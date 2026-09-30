package execution

import (
	"context"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

// A hint only accelerates observation of an already committed input. Lookup or
// delivery failure leaves that input for the normal maintenance scan.
func (w *Worker) hintRuntimeWake(ctx context.Context, session sessions.Session) {
	r := w.runtimes
	if r == nil {
		return
	}
	r.mu.Lock()
	config := r.config
	available := !r.closed && !r.switching
	r.mu.Unlock()
	if !available || config.Suspension == nil || r.ctx.Err() != nil {
		return
	}
	lookup, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	environment, err := w.dispatcher.SessionsReader.GetSessionEnvironment(lookup, session.TenantID, session.ID)
	if err != nil || environment.Initialization != "complete" {
		return
	}
	owner, err := w.dispatcher.DeploymentReader.EnvironmentAllocation(lookup, deployment.AllocationKey{TenantID: session.TenantID, EnvironmentID: environment.ID})
	if err != nil || owner.ProviderKey != config.InstallationID || owner.State != "running" ||
		!owner.CreateSettled || owner.SessionDeleted || owner.Expired {
		return
	}
	switch owner.ComputePhase {
	case "quiescing", "suspending", "suspended", "restoring", "waking":
		select {
		case r.hints(owner.NodeID) <- struct{}{}:
		default:
		}
	}
}

// Keep the ordinary ticker as the recovery guarantee. Hints allow at most one
// extra scan per normal cycle, including when clients repeatedly retry an input.
func runRuntimeMaintenance(ctx context.Context, ticks <-chan time.Time, hints <-chan struct{}, reconcile func(context.Context) error) error {
	drainRuntimeWakeHint(hints)
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := reconcile(ctx); err != nil {
		return err
	}
	extraAllowed := true
	for {
		readyHints := hints
		if !extraAllowed {
			readyHints = nil
		}
		normal := false
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticks:
			normal = true
		case <-readyHints:
			// A simultaneously due tick owns this scan; do not scan twice.
			select {
			case <-ticks:
				normal = true
			default:
			}
		}
		extraAllowed = normal
		if normal {
			// Pending hints are covered by the scan about to start. Hints that
			// arrive during it stay queued for the new cycle's extra scan.
			drainRuntimeWakeHint(hints)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := reconcile(ctx); err != nil {
			return err
		}
	}
}

func drainRuntimeWakeHint(hints <-chan struct{}) {
	select {
	case <-hints:
	default:
	}
}
