package execution

import (
	"context"
	"errors"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/obs/log"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
)

func (w *Worker) StartSandboxReset(ctx context.Context, input deployment.ResetRequest) (deployment.View, error) {
	unlock, err := w.runtimes.lockMutation(ctx)
	if err != nil {
		return deployment.View{}, err
	}
	defer unlock()
	m := w.runtimes
	if err := m.deployment.StartReset(ctx, m.setupInstallationID, input); err != nil {
		return deployment.View{}, err
	}
	return m.committedView(ctx)
}

func (w *Worker) CancelSandboxReset(ctx context.Context, generation uint64) (deployment.View, error) {
	unlock, err := w.runtimes.lockMutation(ctx)
	if err != nil {
		return deployment.View{}, err
	}
	defer unlock()
	m := w.runtimes
	if err := m.deployment.CancelReset(ctx, m.setupInstallationID, generation); err != nil {
		return deployment.View{}, err
	}
	return m.committedView(ctx)
}

// committedView reads the deployment after a committed reset change. The
// caller still holds the mutation gate, so no other Web change interleaves,
// but the gate does not freeze allocation counts or other live observations:
// the response is the current state read after commit. A failed read fails the
// response and never rolls back the change. The read runs on a pooled
// snapshot, so the lease check that follows proves no successor owner wrote
// before it; a lost lease stops this owner.
func (m *runtimeManager) committedView(ctx context.Context) (deployment.View, error) {
	view, err := m.deploymentService.View(ctx)
	if err != nil {
		return deployment.View{}, err
	}
	check, cancel := context.WithTimeout(m.ctx, 5*time.Second)
	defer cancel()
	if err := m.lease.CheckOwnership(check); err != nil {
		select {
		case m.failed <- err:
		default:
		}
		return deployment.View{}, ErrExecutionUnavailable
	}
	return view, nil
}

// resetStep runs only on the manager's uncounted coordinator loop. It must never
// enter m.active, hold a Session/deployment transaction, or call a provider while
// waiting for a deployment drain. Each page has both a row and time bound.
func (m *runtimeManager) resetStep(parent context.Context) error {
	if m.loadDeployment == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	return m.resetPage(parent, ctx)
}

func (m *runtimeManager) resetPage(parent, ctx context.Context) error {
	unlock, err := m.lockMutation(ctx)
	if err != nil {
		if ctx.Err() != nil && parent.Err() == nil {
			return nil
		}
		return err
	}
	defer unlock()
	if err := m.deployment.AdvanceResetDeadline(ctx); err != nil {
		return err
	}
	current, err := m.deploymentService.View(ctx)
	if err != nil {
		return err
	}
	if current.Reset == nil {
		m.resetCursor = ""
		m.resetRequestedAt = time.Time{}
		return nil
	}
	if !current.Reset.RequestedAt.Equal(m.resetRequestedAt) {
		m.resetCursor = ""
		m.resetRequestedAt = current.Reset.RequestedAt
	}
	candidates, err := m.deploymentReader.ResetSessions(ctx, m.resetCursor, current.Reset.Clear == deployment.ResetForce)
	if err != nil {
		return err
	}
	for _, candidate := range candidates {
		if ctx.Err() != nil {
			return nil
		}
		_, err := m.store.ArchiveSandboxResetSession(ctx, candidate.TenantID, candidate.SessionID, current.Generation, current.Reset.RequestedAt)
		m.resetCursor = candidate.SessionID
		if err != nil && !errors.Is(err, store.ErrSandboxResetSessionBusy) && !errors.Is(err, sessions.ErrNotFound) {
			// Do not log a provider body, request, credential or stored provenance.
			log.Warn(ctx, "Sandbox reset archive remains pending", "session_id", candidate.SessionID)
		}
	}
	if len(candidates) < 32 {
		m.resetCursor = ""
	}
	if ctx.Err() != nil {
		return nil
	}
	current, err = m.deploymentService.View(ctx)
	if err != nil {
		return err
	}
	if current.Reset == nil || current.Resources.Allocations != 0 || current.Resources.Pending != 0 {
		return nil
	}
	if err := m.pauseDeployment(ctx); err != nil {
		if recovery := m.restoreCommittedDeployment(); recovery != nil {
			return errors.Join(err, recovery)
		}
		if parent.Err() == nil && ctx.Err() != nil && errors.Is(err, ctx.Err()) {
			// A bounded page may expire during drain. Successful owner-context
			// recovery keeps this durable reset available for the next tick.
			return nil
		}
		return err
	}
	committed, err := m.deployment.CompleteReset(ctx, m.setupInstallationID, current.Generation, current.Reset.RequestedAt)
	if err != nil {
		recovery := m.restoreCommittedDeployment()
		if recovery != nil {
			return errors.Join(err, recovery)
		}
		// The transaction rechecks counts after the drain. A losing recheck
		// leaves the durable clear intact for the next owner tick.
		log.Warn(parent, "Sandbox reset completion remains pending", "generation", current.Generation)
		return nil
	}
	// Publish from the committed generation alone: no read may stand between
	// the commit and retiring the old runtime configuration.
	m.publishEmptyDeployment(m.setupInstallationID, committed)
	m.resetCursor = ""
	return nil
}
