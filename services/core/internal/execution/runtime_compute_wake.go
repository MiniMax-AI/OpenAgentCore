package execution

import (
	"context"
	"errors"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimegateway"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
)

func (r *runtimeLifecycle) wakeCompute(ctx context.Context, p sandbox.CheckpointProvider, owner store.RuntimeAllocation, state runtimeCompute) error {
	if state.Rollback {
		if _, err := p.ResumeCompute(ctx, runtimeReference(owner), state.Current); err != nil {
			return err
		}
	}
	peer, err := authorizedRuntimePeer(ctx, r.store, r.registry, owner.DeviceID)
	if err != nil {
		if !errors.Is(err, sessions.ErrNotFound) && !errors.Is(err, runtimegateway.ErrDeviceNotRegistered) && !errors.Is(err, runtimegateway.ErrSessionClosed) {
			return err
		}
		// This idempotent control signal is fenced by guest PID/start time and the
		// suspension token. It cannot execute or replay an agent request.
		result, err := p.RunCommandCompute(ctx, runtimeReference(owner), state.Current, sandbox.Command{Args: []string{"oac-daemon", "resume", "--control-file", "/run/oac/daemon-suspend.json", "--environment-id", owner.EnvironmentID, "--suspend-id", state.SuspendID}})
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
	// The artifact has been consumed. Never restore it after this generation
	// admits work, even if garbage collection or the final database commit fails.
	if state.Snapshot != nil {
		if err := ignoreComputeAbsent(p.DeleteSnapshot(ctx, runtimeReference(owner), *state.Snapshot)); err != nil {
			return err
		}
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

func (r *runtimeLifecycle) cleanupCompute(ctx context.Context, p sandbox.CheckpointProvider, owner store.RuntimeAllocation, state runtimeCompute) error {
	if err := r.lease.CheckOwnership(ctx); err != nil {
		return err
	}
	// An uncommitted artifact is found by its persisted attempt, never a directory
	// glob. The helper's allocation lock also waits for an earlier unknown call.
	if owner.ComputePhase == "suspending" && state.Snapshot == nil {
		result, err := p.Suspend(ctx, sandbox.SuspendRequest{Reference: runtimeReference(owner), OperationID: state.SuspendID, Source: state.Current, ObserveOnly: true})
		if err != nil && !errors.Is(err, sandbox.ErrNotFound) {
			return err
		}
		if err == nil {
			state.Snapshot = result.Snapshot
		}
	}
	if state.Target != nil {
		if err := ignoreComputeAbsent(p.KillCompute(ctx, runtimeReference(owner), *state.Target)); err != nil {
			return err
		}
	}
	if err := ignoreComputeAbsent(p.KillCompute(ctx, runtimeReference(owner), state.Current)); err != nil {
		return err
	}
	if state.Snapshot != nil {
		if err := ignoreComputeAbsent(p.DeleteSnapshot(ctx, runtimeReference(owner), *state.Snapshot)); err != nil {
			return err
		}
	}
	_, err := r.store.ReleaseRuntimeAllocation(ctx, owner)
	return err
}

// waitRuntimeAwake is called only for live Environment file operations, before
// entering the Worker's work queues. Persisted history/artifact reads bypass it.
func (w *Worker) waitRuntimeAwake(ctx context.Context, environment sessions.Environment) error {
	if w.runtimes == nil {
		return nil
	}
	owner, err := w.admission.GetRuntimeAllocation(ctx, environment.TenantID, environment.ID)
	if errors.Is(err, sessions.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if owner.SessionDeleted || owner.Expired || owner.State == "cleanup_pending" || owner.State == "released" {
		return ErrExecutionUnavailable
	}
	if owner.ComputePhase == "disabled" {
		return nil
	}
	if err := w.admission.TouchRuntimeActivity(ctx, environment.TenantID, environment.ID); err != nil {
		return err
	}
	timer := time.NewTicker(100 * time.Millisecond)
	defer timer.Stop()
	for {
		owner, err = w.admission.GetRuntimeAllocation(ctx, environment.TenantID, environment.ID)
		if err != nil {
			return err
		}
		if owner.SessionDeleted || owner.Expired || owner.State != "running" {
			return ErrExecutionUnavailable
		}
		if owner.ComputePhase == "running" {
			return nil
		}
		select {
		case <-ctx.Done():
			return ErrExecutionUnavailable
		case <-w.stopped:
			return ErrExecutionUnavailable
		case <-timer.C:
		}
	}
}
