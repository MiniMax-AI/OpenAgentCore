package execution

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/gateway"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

// Resident suspension retains the original provider ID. It intentionally does
// not claim a checkpoint or use the snapshot path, which kills its source VM.
func (r *runtimeLifecycle) observeResidentCompute(ctx context.Context, p sandbox.ResidentPauseProvider, owner store.RuntimeAllocation) error {
	var state runtimeCompute
	if !validResidentState(owner, &state) {
		return sandbox.ErrOwnership
	}
	if owner.SessionDeleted || owner.Expired || owner.State == "cleanup_pending" {
		if err := r.store.CheckExecutionOwnership(ctx); err != nil {
			return err
		}
		if err := p.Kill(ctx, runtimeReference(owner)); err != nil {
			return err
		}
		_, err := r.store.ReleaseRuntimeAllocation(ctx, owner)
		return err
	}
	if err := r.store.CheckExecutionOwnership(ctx); err != nil {
		return err
	}
	switch owner.ComputePhase {
	case "running":
		return r.idleResidentCompute(ctx, p, owner, state)
	case "quiescing":
		state.Rollback = true
		next, err := r.saveCompute(ctx, owner, "waking", state, owner.ComputeRetainedUntil)
		if err != nil {
			return err
		}
		return r.wakeResidentCompute(ctx, p, next, state)
	case "suspending":
		// The pause receipt precedes the external call. After an uncertain
		// result, even a running observation cannot prove that the original
		// request will not pause later, so never issue Pause a second time.
		info, err := p.GetInfo(ctx, runtimeReference(owner))
		if err != nil {
			return err
		}
		if !residentInfo(owner, state, info, "paused") {
			return sandbox.ErrComputeUnconfirmed
		}
		_, err = r.saveCompute(ctx, owner, "suspended", state, owner.ComputeRetainedUntil)
		return err
	case "suspended":
		activity, err := r.store.RuntimeActivity(ctx, owner)
		if err != nil || !activity.Busy && !activity.WakeRequested {
			return err
		}
		if err := r.computeCapacityForAllocation(ctx, owner); err != nil {
			return err
		}
		state.RestoreID = uuid.NewString()
		next, err := r.saveCompute(ctx, owner, "restoring", state, owner.ComputeRetainedUntil)
		if err != nil {
			return err
		}
		return r.restoreResidentCompute(ctx, p, next, state)
	case "restoring":
		return r.restoreResidentCompute(ctx, p, owner, state)
	case "waking":
		return r.wakeResidentCompute(ctx, p, owner, state)
	default:
		return sandbox.ErrInvalid
	}
}

func validResidentState(owner store.RuntimeAllocation, state *runtimeCompute) bool {
	return len(owner.ComputeState) > 0 && json.Unmarshal(owner.ComputeState, state) == nil &&
		state.Current.ID != "" && state.Target == nil && state.Snapshot == nil
}

func residentInfo(owner store.RuntimeAllocation, state runtimeCompute, info sandbox.Info, status string) bool {
	return info.Reference == runtimeReference(owner) && info.ProviderID == state.Current.ID &&
		info.State == status && info.BootstrapComplete && info.CreateSettled
}

func (r *runtimeLifecycle) idleResidentCompute(ctx context.Context, p sandbox.ResidentPauseProvider, owner store.RuntimeAllocation, state runtimeCompute) error {
	activity, err := r.store.RuntimeActivity(ctx, owner)
	if err != nil {
		return err
	}
	if activity.WakeRequested {
		return r.store.ClearRuntimeWake(ctx, owner, owner.ComputeActivityAt)
	}
	policy := r.config.Suspension
	if policy == nil || !activity.ReadyToPauseResident(policy.IdleTimeout) {
		info, err := p.Renew(ctx, runtimeReference(owner))
		if err != nil {
			return err
		}
		if !residentInfo(owner, state, info, "running") {
			return sandbox.ErrComputeUnconfirmed
		}
		_, err = r.store.KeepRuntimeAllocation(ctx, owner)
		return err
	}
	info, err := p.GetInfo(ctx, runtimeReference(owner))
	if err != nil {
		return err
	}
	if !residentInfo(owner, state, info, "running") {
		return sandbox.ErrComputeUnconfirmed
	}
	peer, err := authorizedRuntimePeer(ctx, r.store, r.registry, owner.DeviceID)
	if err != nil {
		return err
	}
	state.SuspendID, state.RestoreID, state.Rollback = uuid.NewString(), "", false
	until := activity.ObservedAt.Add(policy.Retention)
	next, err := r.saveCompute(ctx, owner, "quiescing", state, &until)
	if err != nil {
		return err
	}
	result, err := peer.SuspendControl(ctx, proto.TypeEnvironmentQuiesce, proto.EnvironmentSuspendPayload{EnvironmentID: owner.EnvironmentID, SuspendID: state.SuspendID})
	if err != nil {
		return err
	}
	if !result.Accepted {
		if err := r.store.TouchRuntimeActivity(ctx, owner.TenantID, owner.EnvironmentID); err != nil {
			return err
		}
		_, err = r.saveCompute(ctx, next, "running", runtimeCompute{Current: state.Current}, nil)
		return err
	}
	if err := observeRuntimeConnection(ctx, r.store, r.connections, owner.TenantID, owner.EnvironmentID, nil, false); err != nil {
		return err
	}
	suspending, err := r.saveCompute(ctx, next, "suspending", state, &until)
	if errors.Is(err, store.ErrTurnConflict) {
		state.Rollback = true
		next, err = r.saveCompute(ctx, next, "waking", state, &until)
		if err != nil {
			return err
		}
		return r.wakeResidentCompute(ctx, p, next, state)
	}
	if err != nil {
		return err
	}
	info, err = p.Pause(ctx, runtimeReference(owner))
	if err != nil {
		return err
	}
	if !residentInfo(owner, state, info, "paused") {
		return sandbox.ErrComputeUnconfirmed
	}
	_, err = r.saveCompute(ctx, suspending, "suspended", state, &until)
	return err
}

func (r *runtimeLifecycle) restoreResidentCompute(ctx context.Context, p sandbox.ResidentPauseProvider, owner store.RuntimeAllocation, state runtimeCompute) error {
	info, err := p.Resume(ctx, runtimeReference(owner))
	if err != nil {
		return err
	}
	if !residentInfo(owner, state, info, "running") {
		return sandbox.ErrComputeUnconfirmed
	}
	next, err := r.saveCompute(ctx, owner, "waking", state, owner.ComputeRetainedUntil)
	if err != nil {
		return err
	}
	return r.wakeResidentCompute(ctx, p, next, state)
}

func (r *runtimeLifecycle) wakeResidentCompute(ctx context.Context, p sandbox.ResidentPauseProvider, owner store.RuntimeAllocation, state runtimeCompute) error {
	peer, err := authorizedRuntimePeer(ctx, r.store, r.registry, owner.DeviceID)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) && !errors.Is(err, gateway.ErrDeviceNotRegistered) && !errors.Is(err, gateway.ErrSessionClosed) {
			return err
		}
		result, err := p.RunCommand(ctx, runtimeReference(owner), sandbox.Command{Args: []string{"oac-daemon", "resume", "--control-file", "/run/oac/daemon-suspend.json", "--environment-id", owner.EnvironmentID, "--suspend-id", state.SuspendID}})
		if err != nil {
			return err
		}
		if result.ExitCode != 0 {
			return sandbox.ErrComputeUnconfirmed
		}
		timer := time.NewTicker(100 * time.Millisecond)
		defer timer.Stop()
		for {
			peer, err = authorizedRuntimePeer(ctx, r.store, r.registry, owner.DeviceID)
			if err == nil {
				break
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-timer.C:
			}
		}
	}
	result, err := peer.SuspendControl(ctx, proto.TypeEnvironmentResume, proto.EnvironmentSuspendPayload{EnvironmentID: owner.EnvironmentID, SuspendID: state.SuspendID, Rollback: state.Rollback})
	if err != nil {
		return err
	}
	if !result.Accepted {
		return sandbox.ErrComputeUnconfirmed
	}
	next, err := r.saveCompute(ctx, owner, "running", runtimeCompute{Current: state.Current}, nil)
	if err != nil {
		return err
	}
	if err := r.store.ClearRuntimeWake(ctx, next, owner.ComputeActivityAt); err != nil {
		return err
	}
	return r.observeConnection(ctx, next)
}
