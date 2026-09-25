package execution

import (
	"context"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

func (m *runtimeManager) lockMutation(ctx context.Context) (func(), error) {
	if m == nil || m.loadDeployment == nil {
		return nil, store.ErrSandboxDeploymentConflict
	}
	select {
	case m.mutationGate <- struct{}{}:
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
		m.switchDrained = make(chan struct{})
		go m.drainDeployment(m.switchDrained)
	}
	drained := m.switchDrained
	m.mu.Unlock()
	select {
	case <-drained:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-m.ctx.Done():
		return ErrExecutionUnavailable
	}
}

// One drain outlives the initiating HTTP request. A cancelled waiter cannot
// permit a replacement generation while an old caller is still completing.
func (m *runtimeManager) drainDeployment(done chan struct{}) {
	defer close(done)
	for _, gate := range []chan struct{}{m.setupGate, m.inventory} {
		select {
		case gate <- struct{}{}:
			<-gate
		case <-m.ctx.Done():
		}
	}
	m.mu.Lock()
	for _, n := range m.nodes {
		n.lifecycle.stop()
	}
	m.mu.Unlock()
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
	if err := m.store.CheckSandboxDeploymentSwitch(ctx, m.setupInstallationID, input); err != nil {
		return store.RuntimeDeploymentView{}, err
	}
	candidate, err := m.prepareCandidate(ctx, input.SandboxDeploymentSetupRequest)
	if err != nil {
		return store.RuntimeDeploymentView{}, err
	}
	if err := m.pauseDeployment(ctx); err != nil {
		return store.RuntimeDeploymentView{}, err
	}
	input.SandboxDeploymentSetupRequest = withTemplateBuild(input.SandboxDeploymentSetupRequest, candidate)
	result, err := m.store.UpdateSandboxDeployment(ctx, m.setupInstallationID, input)
	if err != nil {
		return store.RuntimeDeploymentView{}, err
	}
	m.publishDeployment(candidate, result)
	return result, nil
}

func (w *Worker) SetSandboxMaintenance(ctx context.Context, input store.SandboxMaintenanceRequest) (store.RuntimeDeploymentView, error) {
	unlock, err := w.runtimes.lockMutation(ctx)
	if err != nil {
		return store.RuntimeDeploymentView{}, err
	}
	defer unlock()
	m := w.runtimes
	if !input.Maintenance {
		expected, err := m.store.GetRuntimeDeployment(ctx)
		if err != nil {
			return store.RuntimeDeploymentView{}, err
		}
		if expected.Generation != input.ExpectedGeneration {
			return store.RuntimeDeploymentView{}, store.ErrSandboxDeploymentConflict
		}
		if err := m.activateDeployment(ctx, expected); err != nil {
			return store.RuntimeDeploymentView{}, err
		}
	}
	return m.store.SetSandboxMaintenance(ctx, m.setupInstallationID, input)
}
