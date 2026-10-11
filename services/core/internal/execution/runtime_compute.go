package execution

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/obs/log"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/providercontract"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimegateway"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/workspacefs"
	"github.com/google/uuid"
)

// RuntimeSuspensionPolicy is the idle suspension policy of a provider that
// suspends sandboxes.
type RuntimeSuspensionPolicy struct {
	IdleTimeout time.Duration
	Retention   time.Duration
}

type runtimeCompute struct {
	Current   sandbox.Compute           `json:"current"`
	Target    *sandbox.Compute          `json:"target,omitempty"`
	Snapshot  *sandbox.SnapshotIdentity `json:"snapshot,omitempty"`
	SuspendID string                    `json:"suspend_id,omitempty"`
	RestoreID string                    `json:"restore_id,omitempty"`
	Rollback  bool                      `json:"rollback,omitempty"`
}

func (r *runtimeLifecycle) saveCompute(ctx context.Context, owner deployment.Allocation, phase string, state runtimeCompute, until *time.Time) (deployment.Allocation, error) {
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
	next, err := r.deployment.SetCompute(ctx, owner, phase, raw, until, idleTimeout)
	if err == nil {
		log.Ctx(ctx).Info("Runtime compute phase saved", "allocation_id", owner.ID, "node_id", owner.NodeID, "from", owner.ComputePhase, "to", phase)
	}
	return next, err
}

// Durations use this Core process's monotonic clock and cover the provider or
// daemon round trip as seen by the lifecycle, including node queueing.
func logComputeStep(ctx context.Context, owner deployment.Allocation, step string, started time.Time, err error) {
	log.Ctx(ctx).Info("Runtime compute step", "allocation_id", owner.ID, "node_id", owner.NodeID, "step", step,
		"duration_ms", time.Since(started).Milliseconds(), "succeeded", err == nil)
}

func (r *runtimeLifecycle) enableCompute(ctx context.Context, owner deployment.Allocation) (err error) {
	defer func() { err = withObservationOwner(owner, err) }()
	p := r.config.Provider
	if err := providercontract.Require(p, "Initial"); err != nil {
		return err
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

func (r *runtimeLifecycle) observeCompute(ctx context.Context, owner deployment.Allocation) (err error) {
	defer func() { err = withObservationOwner(owner, err) }()
	p := r.config.Provider
	if err := providercontract.Require(p, "Initial"); err != nil {
		return err
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

func (r *runtimeLifecycle) idleCompute(ctx context.Context, p sandbox.SandboxProvider, owner deployment.Allocation, state runtimeCompute) (err error) {
	defer func() { err = withObservationOwner(owner, err) }()
	compute, err := p.GetCompute(ctx, runtimeReference(owner), state.Current)
	if errors.Is(err, sandbox.ErrNotFound) {
		return r.releaseLostCompute(ctx, p, owner, state, err)
	}
	if err != nil {
		return err
	}
	if compute.Terminated() {
		if compute.Compute.ID != state.Current.ID || compute.Compute.Name != state.Current.Name || compute.Compute.Generation != state.Current.Generation {
			return sandbox.ErrOwnership
		}
		return r.releaseLostCompute(ctx, p, owner, state, sandbox.ErrComputeUnconfirmed)
	}
	if compute.Status != "running" || !compute.BootstrapComplete {
		return sandbox.ErrComputeUnconfirmed
	}
	peer, err := authorizedRuntimePeer(ctx, r.sessions, r.registry, owner.DeviceID)
	if err != nil {
		return err
	}
	if _, err := r.deployment.CheckRunning(ctx, owner); err != nil {
		return err
	}
	activity, err := r.reader.Activity(ctx, owner.ID)
	if err != nil {
		return err
	}
	if activity.WakeRequested {
		// Clearing only the observed timestamp cannot consume a newer live request.
		return r.deployment.ClearWake(ctx, owner, owner.ComputeActivityAt)
	}
	policy := r.config.Suspension
	if policy == nil || activity.Busy {
		return nil
	}
	state.SuspendID, state.RestoreID, state.Rollback = uuid.NewString(), "", false
	until := activity.ObservedAt.Add(policy.Retention)
	next, err := r.saveCompute(ctx, owner, "quiescing", state, &until)
	if errors.Is(err, deployment.ErrNotIdle) {
		return nil
	}
	if err != nil {
		return err
	}
	owner = next
	result, err := peer.SuspendControl(ctx, proto.TypeEnvironmentQuiesce, proto.EnvironmentSuspendPayload{EnvironmentID: owner.EnvironmentID, SuspendID: state.SuspendID})
	if err != nil {
		return err
	}
	if !result.Accepted {
		// A rejected request did not park the daemon. Reset its idle clock so a
		// continuing file operation is not immediately interrupted by another try.
		if err := r.deployments.TouchActivity(ctx, owner.TenantID, owner.EnvironmentID); err != nil {
			return err
		}
		_, err = r.saveCompute(ctx, next, "running", runtimeCompute{Current: state.Current}, nil)
		return err
	}
	// Publish disconnected only after receiving the daemon's receipt barrier.
	if err := observeRuntimeConnection(ctx, r.sessionExecution, r.connections, owner.TenantID, owner.EnvironmentID, nil, false); err != nil {
		return err
	}
	// The Session-locked phase commit checks pending work and wake requests.
	// A competing request keeps its queue position and resumes this source.
	suspending, err := r.saveCompute(ctx, next, "suspending", state, &until)
	if errors.Is(err, deployment.ErrAllocationConflict) {
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

// releaseLostCompute settles a settled running incarnation that its owning
// node reports absent, stopped or crashed. Only exact cleanup of that
// incarnation, with no live Runtime, no other compute intent and a Session
// that can continue on new compute, releases ownership; every other case
// returns the observation's error.
func (r *runtimeLifecycle) releaseLostCompute(ctx context.Context, p sandbox.SandboxProvider, owner deployment.Allocation, state runtimeCompute, observed error) error {
	if !owner.CreateSettled || owner.State != "running" || state.Current.ID == "" || state.Target != nil || state.Snapshot != nil || state.SuspendID != "" || state.RestoreID != "" || state.Rollback {
		return observed
	}
	if _, err := authorizedRuntimePeer(ctx, r.sessions, r.registry, owner.DeviceID); err == nil {
		return sandbox.ErrComputeUnconfirmed
	} else if !errors.Is(err, sessions.ErrNotFound) && !errors.Is(err, runtimegateway.ErrDeviceNotRegistered) && !errors.Is(err, runtimegateway.ErrSessionClosed) {
		return err
	}
	current, err := r.reader.EnvironmentAllocation(ctx, owner.Key())
	if err != nil {
		return err
	}
	if current.ID != owner.ID || current.State != owner.State || current.ComputePhase != owner.ComputePhase || current.ComputeRevision != owner.ComputeRevision {
		return deployment.ErrAllocationConflict
	}
	retained, err := r.reader.RetainedNativeHistory(ctx, owner.Key())
	if err != nil {
		return err
	}
	if !retained {
		return observed
	}
	if err := r.lease.CheckOwnership(ctx); err != nil {
		return err
	}
	if err := ignoreComputeAbsent(p.KillCompute(ctx, runtimeReference(owner), state.Current)); err != nil {
		return err
	}
	released, err := r.deployment.ReleaseLostCompute(ctx, owner)
	if err != nil {
		return err
	}
	r.clearRuntimeState(released)
	return nil
}

func (r *runtimeLifecycle) captureCompute(ctx context.Context, p sandbox.SandboxProvider, owner deployment.Allocation, state runtimeCompute, observeOnly bool) (err error) {
	defer func() { err = withObservationOwner(owner, err) }()
	started := time.Now()
	result, err := p.Suspend(ctx, sandbox.SuspendRequest{Reference: runtimeReference(owner), OperationID: state.SuspendID, Source: state.Current, Snapshot: state.Snapshot, ObserveOnly: observeOnly})
	logComputeStep(ctx, owner, "suspend", started, err)
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
	if result.Snapshot.Compatibility.Validate() != nil {
		return sandbox.ErrOwnership
	}
	state.Snapshot = result.Snapshot
	// Store the verified artifact before any recovery-path kill. Snapshot failure
	// or an unknown result cannot silently fall back to a cold Environment.
	next, err := r.saveCompute(ctx, owner, "suspending", state, owner.ComputeRetainedUntil)
	if err != nil {
		return err
	}
	owner = next
	if !result.SourceStopped {
		// Native absence alone does not settle capture or publish the archive.
		return sandbox.ErrComputeUnconfirmed
	}
	_, err = r.saveCompute(ctx, next, "suspended", state, next.ComputeRetainedUntil)
	return err
}

func (r *runtimeLifecycle) restoreIdleCompute(ctx context.Context, p sandbox.SandboxProvider, owner deployment.Allocation, state runtimeCompute) (err error) {
	defer func() { err = withObservationOwner(owner, err) }()
	activity, err := r.reader.Activity(ctx, owner.ID)
	if err != nil {
		return err
	}
	if !activity.Busy && !activity.WakeRequested {
		return nil
	}
	if state.Snapshot == nil || state.Target != nil {
		return sandbox.ErrOwnership
	}
	state.RestoreID = uuid.NewString()
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	next, err := r.deployment.BeginRestore(ctx, owner, state.Snapshot.Compatibility, raw)
	if err != nil {
		return err
	}
	if next.NodeID != r.nodeID {
		// The durable route and reservation belong to the target node's lane.
		if r.hintDestination != nil {
			r.hintDestination(next.NodeID)
		}
		return nil
	}
	return r.restoreCompute(ctx, p, next, state, false)
}
func (r *runtimeLifecycle) restoreCompute(ctx context.Context, p sandbox.SandboxProvider, owner deployment.Allocation, state runtimeCompute, observeOnly bool) (err error) {
	defer func() { err = withObservationOwner(owner, err) }()
	if state.Snapshot == nil || state.Snapshot.Compatibility.Validate() != nil || state.Rollback || state.RestoreID == "" {
		return sandbox.ErrOwnership
	}
	if state.Target == nil {
		// NewCompute derives identity without starting native execution. A crash
		// before this commit can safely repeat derivation on the reserved target.
		target, err := p.NewCompute(ctx, runtimeReference(owner), state.Current.Generation+1, state.Snapshot)
		if err != nil {
			return err
		}
		state.Target = &target
		next, err := r.saveCompute(ctx, owner, "restoring", state, owner.ComputeRetainedUntil)
		if err != nil {
			return err
		}
		owner, observeOnly = next, false
	}
	spec, err := r.workspaceSpecification(ctx, owner.Key(), owner.ID)
	if err != nil {
		return err
	}
	var workspace *workspacefs.Binding
	if spec.Workspace != nil {
		workspace, err = r.workspaces.GetReady(ctx, owner.TenantID, owner.EnvironmentID, r.config.WorkspaceRequirements)
		if err == nil && workspace == nil {
			err = workspacefs.ErrNotFound
		}
		if err != nil {
			return err
		}
	}
	started := time.Now()
	result, err := p.Resume(ctx, sandbox.ResumeRequest{Workspace: workspace, Reference: runtimeReference(owner), OperationID: state.RestoreID, Snapshot: *state.Snapshot, Target: *state.Target, ObserveOnly: observeOnly})
	logComputeStep(ctx, owner, "resume", started, err)
	if err != nil {
		return err
	}
	if result.RestoreAttemptClosed != "" {
		if !result.ClosesRestoreAttempt(sandbox.ResumeRequest{OperationID: state.RestoreID, Snapshot: *state.Snapshot, Target: *state.Target, ObserveOnly: observeOnly}) {
			return sandbox.ErrOwnership
		}
		// Only an exact, durably closed attempt with no native dispatch can be replaced.
		state.RestoreID = uuid.NewString()
		next, err := r.saveCompute(ctx, owner, "restoring", state, owner.ComputeRetainedUntil)
		if err != nil {
			return err
		}
		return r.restoreCompute(ctx, p, next, state, false)
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
