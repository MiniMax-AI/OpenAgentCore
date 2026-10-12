package execution

import (
	"context"
	"errors"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment/placement"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimegateway"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

func (r *runtimeLifecycle) wakeCompute(ctx context.Context, p sandbox.SandboxProvider, owner deployment.Allocation, state runtimeCompute) (err error) {
	defer func() { err = withObservationOwner(owner, err) }()
	if state.Rollback {
		started := time.Now()
		_, err := p.ResumeCompute(ctx, runtimeReference(owner), state.Current)
		logComputeStep(ctx, owner, "resume_compute", started, err)
		if err != nil {
			return err
		}
	}
	peer, err := authorizedRuntimePeer(ctx, r.sessions, r.registry, owner.DeviceID)
	if err != nil {
		if !errors.Is(err, sessions.ErrNotFound) && !errors.Is(err, runtimegateway.ErrDeviceNotRegistered) && !errors.Is(err, runtimegateway.ErrSessionClosed) {
			return err
		}
		// This idempotent control signal is fenced by guest PID/start time and the
		// suspension token. It cannot execute or replay an agent request.
		started := time.Now()
		result, err := p.RunCommandCompute(ctx, runtimeReference(owner), state.Current, sandbox.Command{Args: []string{"oac-daemon", "resume", "--control-file", "/run/oac/daemon-suspend.json", "--environment-id", owner.EnvironmentID, "--suspend-id", state.SuspendID}})
		logComputeStep(ctx, owner, "wake_command", started, err)
		if err != nil {
			return err
		}
		if result.ExitCode != 0 {
			return sandbox.ErrComputeUnconfirmed
		}
		started = time.Now()
		timer := time.NewTicker(100 * time.Millisecond)
		defer timer.Stop()
		for {
			peer, err = authorizedRuntimePeer(ctx, r.sessions, r.registry, owner.DeviceID)
			if err == nil {
				break
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-timer.C:
			}
		}
		logComputeStep(ctx, owner, "peer_authorized", started, nil)
	}
	started := time.Now()
	result, err := peer.SuspendControl(ctx, proto.TypeEnvironmentResume, proto.EnvironmentSuspendPayload{EnvironmentID: owner.EnvironmentID, SuspendID: state.SuspendID, Rollback: state.Rollback})
	logComputeStep(ctx, owner, "environment_resume", started, err)
	if err != nil {
		return err
	}
	if !result.Accepted {
		return sandbox.ErrComputeUnconfirmed
	}
	// The artifact has been consumed. Never restore it after this generation
	// admits work, even if garbage collection or the final database commit fails.
	if state.Snapshot != nil {
		started = time.Now()
		err := ignoreComputeAbsent(p.DeleteSnapshot(ctx, runtimeReference(owner), *state.Snapshot))
		logComputeStep(ctx, owner, "delete_snapshot", started, err)
		if err != nil {
			return err
		}
	}
	next, err := r.saveCompute(ctx, owner, "running", runtimeCompute{Current: state.Current}, nil)
	if err != nil {
		return err
	}
	observedActivity := owner.ComputeActivityAt
	owner = next
	if err := r.deployment.ClearWake(ctx, next, observedActivity); err != nil {
		return err
	}
	return r.observeConnection(ctx, next)
}

func (r *runtimeLifecycle) cleanupCompute(ctx context.Context, p sandbox.SandboxProvider, owner deployment.Allocation, state runtimeCompute) error {
	if err := r.lease.CheckOwnership(ctx); err != nil {
		return err
	}
	if owner.ComputePhase == "suspended" && state.Snapshot != nil && owner.NodeID != "" {
		next, err := r.deployment.RelocateCleanup(ctx, owner, state.Snapshot.Compatibility)
		if err != nil {
			return err
		}
		if next.NodeID != r.nodeID {
			if r.hintDestination != nil {
				r.hintDestination(next.NodeID)
			}
			return nil
		}
		owner = next
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
	if owner.ComputePhase != "suspended" && owner.ComputePhase != "restoring" {
		if err := ignoreComputeAbsent(p.KillCompute(ctx, runtimeReference(owner), state.Current)); err != nil {
			return err
		}
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
func (w *Worker) waitRuntimeAwake(ctx context.Context, environment sessions.Environment) (err error) {
	// A deadline can arrive inside a read as well as between polling ticks.
	// Both paths report the same unavailable outcome to the live file caller.
	defer func() {
		if ctx.Err() != nil && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
			err = ErrExecutionUnavailable
		}
	}()
	key := deployment.AllocationKey{TenantID: environment.TenantID, EnvironmentID: environment.ID}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	touched := ""
	for {
		owner, err := w.dispatcher.DeploymentReader.EnvironmentAllocation(ctx, key)
		if errors.Is(err, deployment.ErrNotFound) {
			return w.waitEnvironmentInitialization(ctx, environment)
		}
		if err != nil {
			return err
		}
		if owner.SessionDeleted {
			return ErrExecutionUnavailable
		}
		if owner.State == "released" || owner.State == "cleanup_pending" || owner.Expired {
			// Public archive and deletion remain terminal. A qualified compute-only
			// expiry leaves the same Environment live while the old writer is fenced.
			current, err := w.dispatcher.SessionsReader.GetEnvironment(ctx, environment.TenantID, environment.ID)
			if err != nil {
				return err
			}
			if current.Status == "failed" || current.Status == "expired" {
				return ErrExecutionUnavailable
			}
			if owner.State == "released" {
				qualified, err := w.dispatcher.DeploymentReader.RetainedNativeHistory(ctx, key)
				if err != nil {
					return err
				}
				if !qualified {
					return ErrExecutionUnavailable
				}
				// A qualified released allocation retains the existing wake intent.
				// The common placement scan orders it with pending Session input.
				if touched != owner.ID {
					if err := w.dispatcher.Deployment.TouchActivity(ctx, environment.TenantID, environment.ID); err != nil {
						return err
					}
					touched = owner.ID
					w.wakeScheduler()
				}
				next, err := w.ProvisionEnvironment(ctx, environment.TenantID, environment.ID, owner.ProviderKey)
				if err != nil && next.ID == "" && !errors.Is(err, placement.ErrNodeUnavailable) && !errors.Is(err, placement.ErrNodesPreparing) && !errors.Is(err, deployment.ErrAllocationConflict) {
					return err
				}
			}
		} else if owner.State == "running" && owner.CreateSettled && (owner.ComputePhase == "disabled" || owner.ComputePhase == "running") {
			// Bootstrap completion precedes daemon registration and its first
			// capability declaration. File work must wait for both receipts.
			peer, peerErr := w.dispatcher.authorizedPeer(ctx, owner.DeviceID)
			if peerErr == nil {
				session, err := w.dispatcher.SessionsReader.GetSession(ctx, environment.TenantID, environment.SessionID)
				if err != nil {
					return err
				}
				kind, found, known := peer.AgentKindStatus(session.Engine)
				if known {
					if !found || !kind.Available {
						return ErrExecutionUnavailable
					}
					return w.waitEnvironmentInitialization(ctx, environment)
				}
			} else if !errors.Is(peerErr, sessions.ErrNotFound) && !errors.Is(peerErr, runtimegateway.ErrDeviceNotRegistered) && !errors.Is(peerErr, runtimegateway.ErrSessionClosed) {
				return peerErr
			}
		} else if owner.State == "running" && touched != owner.ID {
			if err = w.dispatcher.Deployment.TouchActivity(ctx, environment.TenantID, environment.ID); err != nil {
				return err
			}
			touched = owner.ID
			if w.runtimes != nil {
				select {
				case w.runtimes.hints(owner.NodeID) <- struct{}{}:
				default:
				}
			}
		}
		select {
		case <-ctx.Done():
			return ErrExecutionUnavailable
		case <-w.stopped:
			return ErrExecutionUnavailable
		case <-ticker.C:
		}
	}
}

// A live socket and its capability declaration precede Environment setup. Files
// share the caller's existing budget while waiting for that durable completion;
// they never reserve a worker slot or send preparation during initialization.
func (w *Worker) waitEnvironmentInitialization(ctx context.Context, environment sessions.Environment) error {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		current, err := w.dispatcher.SessionsReader.GetEnvironment(ctx, environment.TenantID, environment.ID)
		if err != nil {
			return err
		}
		if current.SessionID != environment.SessionID {
			return sessions.ErrNotFound
		}
		switch current.Initialization {
		case "complete":
			return nil
		case "pending", "running":
		default:
			return ErrExecutionUnavailable
		}
		select {
		case <-ctx.Done():
			return ErrExecutionUnavailable
		case <-w.stopped:
			return ErrExecutionUnavailable
		case <-ticker.C:
		}
	}
}
