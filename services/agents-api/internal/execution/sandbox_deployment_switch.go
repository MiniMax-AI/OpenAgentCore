package execution

import (
	"context"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

func (m *runtimeManager) lockMutation(ctx context.Context) (func(), error) {
	if m == nil || m.loadDeployment == nil {
		return nil, store.ErrSandboxDeploymentConflict
	}
	m.mu.Lock()
	closed := m.closed
	m.mu.Unlock()
	if closed {
		return nil, ErrExecutionUnavailable
	}
	select {
	case m.mutationGate <- struct{}{}:
		m.mu.Lock()
		closed := m.closed
		m.mu.Unlock()
		if closed {
			<-m.mutationGate
			return nil, ErrExecutionUnavailable
		}
		return func() { <-m.mutationGate }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-m.ctx.Done():
		return nil, ErrExecutionUnavailable
	}
}

// pauseDeployment blocks new callers before waiting for the existing accounting.
// The coordinator remains alive; no database lock spans cancellation or draining.
func (m *runtimeManager) pauseDeployment(ctx context.Context) error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return ErrExecutionUnavailable
	}
	if !m.switching {
		m.switching = true
		m.switchDrained = &deploymentDrain{done: make(chan struct{})}
		go m.drainDeployment(m.switchDrained)
	}
	drained := m.switchDrained
	m.mu.Unlock()
	select {
	case <-drained.done:
		return drained.err
	case <-ctx.Done():
		return ctx.Err()
	case <-m.ctx.Done():
		return ErrExecutionUnavailable
	}
}

// One drain outlives the initiating HTTP request. A cancelled waiter cannot
// permit a replacement generation while an old caller is still completing.
type deploymentDrain struct {
	done chan struct{}
	err  error // Published by closing done; immutable afterwards.
}

func (m *runtimeManager) drainDeployment(result *deploymentDrain) {
	defer close(result.done)
	for _, gate := range []chan struct{}{m.setupGate, m.inventory} {
		select {
		case gate <- struct{}{}:
			<-gate
		case <-m.ctx.Done():
		}
	}
	m.mu.Lock()
	nodes := make([]*runtimeNode, 0, len(m.nodes))
	for _, n := range m.nodes {
		nodes = append(nodes, n)
	}
	m.mu.Unlock()
	if result.err = m.cancelLifecycles(nodes); result.err != nil {
		return
	}
	m.active.Wait()
}

// activateDeployment is serialized with every Web configuration mutation. The
// loader reads the committed generation and publishes the same server snapshot.
func (m *runtimeManager) activateDeployment(ctx context.Context, expected store.RuntimeDeploymentView) error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return ErrExecutionUnavailable
	}
	if !m.switching && m.config.Provider != nil && m.config.Generation == expected.Generation {
		m.mu.Unlock()
		return nil
	}
	switching := m.switching
	m.mu.Unlock()
	if switching {
		if err := m.pauseDeployment(ctx); err != nil {
			return err
		}
	}
	config, err := m.loadDeployment(ctx)
	if err != nil {
		return err
	}
	if config == nil && expected.Provider == "" {
		m.publishEmptyDeployment(expected)
		return nil
	}
	if config == nil || config.InstallationID != expected.InstallationID || config.Generation != expected.Generation || config.Mode != expected.Mode || config.ProviderKind != expected.Provider || config.loadDeployment != nil || config.LocalNodeID != "" {
		return sandbox.ErrInvalid
	}
	copied, err := validatedRuntimeProvider(config, m.registry)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || ctx.Err() != nil {
		return ErrExecutionUnavailable
	}
	// Switching was only admitted with no retained or pending resources.
	if m.switching {
		m.nodes = make(map[string]*runtimeNode)
	}
	m.config = copied
	m.switching = false
	m.switchDrained = nil
	return nil
}

func (w *Worker) UpdateSandboxDeployment(ctx context.Context, input store.SandboxDeploymentUpdateRequest) (store.RuntimeDeploymentView, error) {
	unlock, err := w.runtimes.lockMutation(ctx)
	if err != nil {
		return store.RuntimeDeploymentView{}, err
	}
	defer unlock()
	m := w.runtimes
	input, unchanged, err := m.store.ClassifySandboxDeploymentChange(ctx, m.setupInstallationID, input)
	if err != nil {
		return store.RuntimeDeploymentView{}, err
	}
	if unchanged {
		return m.store.GetRuntimeDeployment(ctx)
	}
	candidate, err := m.prepareCandidate(ctx, input.SandboxDeploymentSetupRequest)
	if err != nil {
		return store.RuntimeDeploymentView{}, err
	}
	if input.HasCredential() || candidate.VerifyCredential != nil {
		if candidate.VerifyCredential == nil || candidate.FenceCredential == nil {
			return store.RuntimeDeploymentView{}, ErrExecutionUnavailable
		}
		if err := candidate.VerifyCredential(ctx); err != nil {
			return store.RuntimeDeploymentView{}, err
		}
		if input.ReplacesCredential() {
			fenceCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			release, err := candidate.FenceCredential(fenceCtx)
			if err != nil {
				return store.RuntimeDeploymentView{}, err
			}
			defer release()
			// The final scan includes allocations admitted during preliminary verification.
			if err := candidate.VerifyCredential(fenceCtx); err != nil {
				return store.RuntimeDeploymentView{}, err
			}
		}
	}
	input.SandboxDeploymentSetupRequest = *candidate.Selection
	result, err := m.store.UpdateSandboxDeployment(ctx, m.setupInstallationID, input)
	if err != nil {
		return store.RuntimeDeploymentView{}, err
	}
	m.publishDeployment(candidate, result)
	return result, nil
}

// Recovery belongs to the owner, not a cancelled HTTP request. Failure keeps the
// drain barrier closed and stops the owner rather than admitting an unknown provider.
func (m *runtimeManager) restoreCommittedDeployment() error {
	ctx, cancel := context.WithTimeout(m.ctx, 5*time.Second)
	defer cancel()
	committed, err := m.store.GetRuntimeDeployment(ctx)
	if err == nil {
		err = m.activateDeployment(ctx, committed)
	}
	if err != nil {
		select {
		case m.failed <- err:
		default:
		}
	}
	return err
}

// Empty-state publication is infallible after commit, even if the request was
// cancelled. Loader, preparer and installation ownership stay on the manager.
func (m *runtimeManager) publishEmptyDeployment(committed store.RuntimeDeploymentView) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.config = RuntimeProvider{InstallationID: committed.InstallationID, Generation: committed.Generation}
	m.nodes = make(map[string]*runtimeNode)
	if m.publishUnconfigured != nil {
		m.publishUnconfigured(committed.Generation)
	}
	m.switching = false
	m.switchDrained = nil
}
