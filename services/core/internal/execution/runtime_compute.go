package execution

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
	"github.com/google/uuid"
)

// RuntimeSuspensionPolicy applies only to an explicitly qualified single-host
// provider. Fixed guest sizing plus MaxActive bounds reserved CPU and memory.
type RuntimeSuspensionPolicy struct {
	IdleTimeout time.Duration
	Retention   time.Duration
	MaxActive   int
	MaxRetained int
}

type runtimeCompute struct {
	Current   sandbox.Compute           `json:"current"`
	Target    *sandbox.Compute          `json:"target,omitempty"`
	Snapshot  *sandbox.SnapshotIdentity `json:"snapshot,omitempty"`
	SuspendID string                    `json:"suspend_id,omitempty"`
	RestoreID string                    `json:"restore_id,omitempty"`
	Rollback  bool                      `json:"rollback,omitempty"`
}

func (r *runtimeLifecycle) computeCapacity(ctx context.Context, key string) error {
	policy := r.config.Suspension
	if key != r.config.InstallationID {
		return sandbox.ErrOwnership
	}
	if policy == nil {
		return nil
	}
	count, err := r.store.CountRuntimeComputeReservations(ctx, key)
	if err != nil {
		return err
	}
	if count >= int64(policy.MaxActive) {
		return ErrExecutionUnavailable
	}
	return nil
}
func (r *runtimeLifecycle) saveCompute(ctx context.Context, owner store.RuntimeAllocation, phase string, state runtimeCompute, until *time.Time) (store.RuntimeAllocation, error) {
	raw, err := json.Marshal(state)
	if err != nil {
		return owner, err
	}
	idleTimeout := time.Duration(0)
	if phase == "quiescing" {
		policy := r.config.Suspension
		if policy == nil {
			return owner, sandbox.ErrInvalid
		}
		idleTimeout = policy.IdleTimeout
	}
	return r.store.SetRuntimeCompute(ctx, owner, phase, raw, until, idleTimeout)
}
func (r *runtimeLifecycle) enableCompute(ctx context.Context, owner store.RuntimeAllocation) error {
	p, capabilityErr := sandbox.Checkpoint(r.config.Provider)
	if capabilityErr != nil {
		return capabilityErr
	}
	initial, err := p.Initial(ctx, runtimeReference(owner))
	if err != nil {
		return err
	}
	state, err := p.GetCompute(ctx, runtimeReference(owner), initial)
	if err != nil {
		return err
	}
	if state.Status != "running" || !state.BootstrapComplete || state.Compute.ID == "" {
		return sandbox.ErrComputeUnconfirmed
	}
	_, err = r.saveCompute(ctx, owner, "running", runtimeCompute{Current: state.Compute}, nil)
	return err
}

func (r *runtimeLifecycle) observeCompute(ctx context.Context, owner store.RuntimeAllocation) error {
	p, capabilityErr := sandbox.Checkpoint(r.config.Provider)
	if capabilityErr != nil {
		return capabilityErr
	}
	var state runtimeCompute
	if json.Unmarshal(owner.ComputeState, &state) != nil || state.Current.ID == "" {
		return sandbox.ErrOwnership
	}
	if owner.SessionDeleted || owner.Expired || owner.State == "cleanup_pending" {
		return r.cleanupCompute(ctx, p, owner, state)
	}
	if err := r.lease.CheckOwnership(ctx); err != nil {
		return err
	}
	switch owner.ComputePhase {
	case "running":
		return r.idleCompute(ctx, p, owner, state)
	case "quiescing":
		// A lost quiesce acknowledgement never authorizes a snapshot. Wake the
		// original source and retain its files; no user request is sent again.
		state.Rollback = true
		next, err := r.saveCompute(ctx, owner, "waking", state, owner.ComputeRetainedUntil)
		if err != nil {
			return err
		}
		return r.wakeCompute(ctx, p, next, state)
	case "suspending":
		return r.captureCompute(ctx, p, owner, state, true)
	case "suspended":
		return r.restoreIdleCompute(ctx, p, owner, state)
	case "restoring":
		return r.restoreCompute(ctx, p, owner, state, true)
	case "waking":
		return r.wakeCompute(ctx, p, owner, state)
	default:
		return sandbox.ErrInvalid
	}
}

func (r *runtimeLifecycle) idleCompute(ctx context.Context, p sandbox.CheckpointProvider, owner store.RuntimeAllocation, state runtimeCompute) error {
	compute, err := p.GetCompute(ctx, runtimeReference(owner), state.Current)
	if err != nil {
		return err
	}
	if compute.Status != "running" || !compute.BootstrapComplete {
		return sandbox.ErrComputeUnconfirmed
	}
	peer, err := authorizedRuntimePeer(ctx, r.store, r.registry, owner.DeviceID)
	if err != nil {
		return err
	}
	if _, err := r.store.KeepRuntimeAllocation(ctx, owner); err != nil {
		return err
	}
	activity, err := r.store.RuntimeActivity(ctx, owner)
	if err != nil {
		return err
	}
	if activity.WakeRequested {
		// Clearing only the observed timestamp cannot consume a newer live request.
		return r.store.ClearRuntimeWake(ctx, owner, owner.ComputeActivityAt)
	}
	policy := r.config.Suspension
	if policy == nil || !activity.ReadyToSuspend(policy.IdleTimeout) {
		return nil
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
		// A rejected request did not park the daemon. Reset its idle clock so a
		// continuing file operation is not immediately interrupted by another try.
		if err := r.store.TouchRuntimeActivity(ctx, owner.TenantID, owner.EnvironmentID); err != nil {
			return err
		}
		_, err = r.saveCompute(ctx, next, "running", runtimeCompute{Current: state.Current}, nil)
		return err
	}
	// Publish disconnected only after receiving the daemon's receipt barrier.
	if err := observeRuntimeConnection(ctx, r.store, r.connections, owner.TenantID, owner.EnvironmentID, nil, false); err != nil {
		return err
	}
	// The Session-locked phase commit checks pending work and wake requests.
	// A competing request keeps its queue position and resumes this source.
	suspending, err := r.saveCompute(ctx, next, "suspending", state, &until)
	if errors.Is(err, sessions.ErrTurnConflict) {
		state.Rollback = true
		next, err = r.saveCompute(ctx, next, "waking", state, &until)
		if err != nil {
			return err
		}
		return r.wakeCompute(ctx, p, next, state)
	}
	if err != nil {
		return err
	}
	return r.captureCompute(ctx, p, suspending, state, false)
}

func (r *runtimeLifecycle) captureCompute(ctx context.Context, p sandbox.CheckpointProvider, owner store.RuntimeAllocation, state runtimeCompute, observeOnly bool) error {
	result, err := p.Suspend(ctx, sandbox.SuspendRequest{Reference: runtimeReference(owner), OperationID: state.SuspendID, Source: state.Current, Snapshot: state.Snapshot, ObserveOnly: observeOnly})
	if err != nil {
		return err
	}
	if result.Compute.ID != state.Current.ID {
		return sandbox.ErrOwnership
	}
	if result.Snapshot == nil {
		if !observeOnly || result.SourceStopped || (result.Status != "running" && result.Status != "paused") {
			return sandbox.ErrComputeUnconfirmed
		}
		state.Rollback = true
		next, err := r.saveCompute(ctx, owner, "waking", state, owner.ComputeRetainedUntil)
		if err != nil {
			return err
		}
		return r.wakeCompute(ctx, p, next, state)
	}
	state.Snapshot = result.Snapshot
	// Store the verified artifact before any recovery-path kill. Snapshot failure
	// or an unknown result cannot silently fall back to a cold Environment.
	next, err := r.saveCompute(ctx, owner, "suspending", state, owner.ComputeRetainedUntil)
	if err != nil {
		return err
	}
	if err := ignoreComputeAbsent(p.KillCompute(ctx, runtimeReference(owner), state.Current)); err != nil {
		return err
	}
	_, err = r.saveCompute(ctx, next, "suspended", state, next.ComputeRetainedUntil)
	return err
}

func (r *runtimeLifecycle) restoreIdleCompute(ctx context.Context, p sandbox.CheckpointProvider, owner store.RuntimeAllocation, state runtimeCompute) error {
	activity, err := r.store.RuntimeActivity(ctx, owner)
	if err != nil {
		return err
	}
	if !activity.Busy && !activity.WakeRequested {
		return nil
	}
	if err := r.computeCapacityForAllocation(ctx, owner); err != nil {
		return err
	}
	if state.Snapshot == nil || state.Target != nil {
		return sandbox.ErrOwnership
	}
	target, err := p.NewCompute(ctx, runtimeReference(owner), state.Current.Generation+1, state.Snapshot)
	if err != nil {
		return err
	}
	state.Target, state.RestoreID = &target, uuid.NewString()
	next, err := r.saveCompute(ctx, owner, "restoring", state, owner.ComputeRetainedUntil)
	if err != nil {
		return err
	}
	return r.restoreCompute(ctx, p, next, state, false)
}
func (r *runtimeLifecycle) restoreCompute(ctx context.Context, p sandbox.CheckpointProvider, owner store.RuntimeAllocation, state runtimeCompute, observeOnly bool) error {
	if state.Target == nil || state.Snapshot == nil || state.Rollback {
		return sandbox.ErrOwnership
	}
	result, err := p.Resume(ctx, sandbox.ResumeRequest{Reference: runtimeReference(owner), OperationID: state.RestoreID, Snapshot: *state.Snapshot, Target: *state.Target, ObserveOnly: observeOnly})
	if err != nil {
		return err
	}
	if result.Status != "running" || result.Compute.ID == "" {
		return sandbox.ErrComputeUnconfirmed
	}
	state.Current, state.Target = result.Compute, nil
	// This commit consumes the snapshot before opening daemon admission. Recovery
	// from waking can only reconnect this generation; it cannot restore again.
	next, err := r.saveCompute(ctx, owner, "waking", state, owner.ComputeRetainedUntil)
	if err != nil {
		return err
	}
	return r.wakeCompute(ctx, p, next, state)
}

func ignoreComputeAbsent(err error) error {
	if errors.Is(err, sandbox.ErrNotFound) {
		return nil
	}
	return err
}

// Node-backed restores reserve capacity atomically in SetRuntimeCompute.
func (r *runtimeLifecycle) computeCapacityForAllocation(ctx context.Context, owner store.RuntimeAllocation) error {
	if owner.NodeID != "" {
		return nil
	}
	return r.computeCapacity(ctx, owner.ProviderKey)
}
