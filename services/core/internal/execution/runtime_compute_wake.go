package execution

import (
	"context"
	"errors"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

func (r *runtimeLifecycle) wakeCompute(ctx context.Context, p sandbox.SandboxProvider, owner deployment.Allocation, state runtimeCompute) error {
	if state.Rollback {
		if _, err := p.ResumeCompute(ctx, runtimeReference(owner), state.Current); err != nil {
			return err
		}
	}
	// The resumed sandbox serves again before its Runtime resumes the
	// Environment. A later pass retries a wake whose sandbox or agent host is
	// not connected.
	if err := r.waitServing(ctx, owner); err != nil {
		return err
	}
	bound, err := r.allocationAssignment(ctx, owner)
	if err != nil {
		return err
	}
	// Resume carries the reference that quiesced the Runtime; a quiesced
	// Runtime admits nothing else, so it is not bound again.
	peer, err := authorizedRuntimePeer(ctx, r.sessions, r.registry, bound.ID)
	if err != nil {
		return err
	}
	result, err := peer.SuspendControl(ctx, proto.TypeEnvironmentResume, bound.Assignment, proto.EnvironmentSuspendPayload{EnvironmentID: owner.EnvironmentID, SuspendID: state.SuspendID, Rollback: state.Rollback})
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
	// The Worker's pass publishes the Environment connected again.
	return r.deployment.ClearWake(ctx, next, owner.ComputeActivityAt)
}

// waitServing waits, for at most 30 seconds, until the relay holds the serve
// peer of the allocation's Link resource.
func (r *runtimeLifecycle) waitServing(ctx context.Context, owner deployment.Allocation) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	timer := time.NewTicker(100 * time.Millisecond)
	defer timer.Stop()
	for !r.links.Serving(serveResource(owner).Ref()) {
		select {
		case <-ctx.Done():
			return sandbox.ErrComputeUnconfirmed
		case <-timer.C:
		}
	}
	return nil
}

func (r *runtimeLifecycle) cleanupCompute(ctx context.Context, p sandbox.SandboxProvider, owner deployment.Allocation, state runtimeCompute) error {
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
	_, err := r.deployment.ReleaseAllocation(ctx, owner)
	return err
}

// waitRuntimeAwake is called only for live Environment file operations, before
// entering the Worker's work queues. Persisted history/artifact reads bypass it.
func (w *Worker) waitRuntimeAwake(ctx context.Context, environment sessions.Environment) error {
	if w.runtimes == nil {
		return nil
	}
	key := deployment.AllocationKey{TenantID: environment.TenantID, EnvironmentID: environment.ID}
	owner, err := w.dispatcher.DeploymentReader.EnvironmentAllocation(ctx, key)
	if errors.Is(err, deployment.ErrNotFound) {
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
	if err := w.dispatcher.Deployment.TouchActivity(ctx, environment.TenantID, environment.ID); err != nil {
		return err
	}
	timer := time.NewTicker(100 * time.Millisecond)
	defer timer.Stop()
	for {
		owner, err = w.dispatcher.DeploymentReader.EnvironmentAllocation(ctx, key)
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
