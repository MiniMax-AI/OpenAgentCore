package execution

import (
	"context"
	"errors"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

// NewDeferredRuntimeProvider enables Web setup for one fixed installation. The
// loader returns nil until selection, then the committed immutable generation.
// Replacement is serialized by the deployment maintenance and drain flow.
func NewDeferredRuntimeProvider(installationID string, load func(context.Context) (*RuntimeProvider, error)) *RuntimeProvider {
	return &RuntimeProvider{InstallationID: installationID, loadDeployment: load}
}

func (w *Worker) InitializeSandboxDeployment(ctx context.Context, input store.SandboxDeploymentSetupRequest) (store.RuntimeDeploymentView, error) {
	unlock, err := w.runtimes.lockMutation(ctx)
	if err != nil {
		return store.RuntimeDeploymentView{}, err
	}
	defer unlock()
	return w.dispatcher.Store.InitializeSandboxDeployment(ctx, w.runtimes.setupInstallationID, input)
}

// ensureDeployment serializes the first configuration read without holding the
// node map lock across database access. All node workers copy this same snapshot.
func (m *runtimeManager) ensureDeployment(parent context.Context) (bool, error) {
	if m.loadDeployment == nil {
		return true, nil
	}
	ctx, finish, err := m.enter(parent)
	if err != nil {
		return false, err
	}
	defer finish()
	select {
	case m.setupGate <- struct{}{}:
	case <-ctx.Done():
		return false, ctx.Err()
	}
	defer func() { <-m.setupGate }()
	m.mu.Lock()
	ready := m.config.Provider != nil
	m.mu.Unlock()
	if ready {
		return true, nil
	}
	config, err := m.loadDeployment(ctx)
	// A missing local provider dependency blocks hosted execution, not the
	// administrator's recovery API. The existing scan can load it after repair.
	// Storage and ownership failures still stop the execution owner.
	if errors.Is(err, ErrExecutionUnavailable) {
		return false, nil
	}
	if err != nil || config == nil {
		return false, err
	}
	if config.InstallationID != m.setupInstallationID || config.LocalNodeID != "" || config.loadDeployment != nil || (config.ProviderKind != "docker" && config.ProviderKind != "microsandbox" && config.ProviderKind != "e2b") {
		return false, sandbox.ErrInvalid
	}
	copied, err := validatedRuntimeProvider(config, m.registry)
	if err != nil {
		return false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.switching {
		return false, errRuntimeTransition
	}
	if m.closed || ctx.Err() != nil {
		return false, ErrExecutionUnavailable
	}
	m.config = copied
	return true, nil
}
