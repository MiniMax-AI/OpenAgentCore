package execution

import (
	"context"
	"errors"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

// preparedRuntimeDeployment is validated but not yet committed or published.
type preparedRuntimeDeployment struct {
	Config    *RuntimeProvider
	Selection sandbox.Selection
	Setup     deployment.Setup
}

func (w *Worker) InitializeSandboxDeployment(ctx context.Context, input sandbox.Selection) (deployment.View, error) {
	unlock, err := w.runtimes.lockMutation(ctx)
	if err != nil {
		return deployment.View{}, err
	}
	defer unlock()
	m := w.runtimes
	if err := m.deployment.CheckSetup(ctx, m.setupInstallationID, input); err != nil {
		return deployment.View{}, err
	}
	candidate, err := m.prepareCandidate(ctx, input)
	if err != nil {
		return deployment.View{}, err
	}
	// An idempotent setup retry can arrive after an interrupted replacement.
	// It must not reopen admission while that replacement is still draining.
	m.mu.Lock()
	switching := m.switching
	m.mu.Unlock()
	if switching {
		if err := m.pauseDeployment(ctx); err != nil {
			return deployment.View{}, err
		}
	}
	result, err := m.deployment.Initialize(ctx, m.setupInstallationID, candidate.Selection)
	if err != nil {
		return deployment.View{}, err
	}
	m.publishDeployment(candidate, result)
	return result, nil
}

// ensureDeployment serializes the first configuration read without holding the
// node map lock across database access. All node workers copy this same snapshot.
func (m *runtimeManager) ensureDeployment(parent context.Context) (bool, error) {
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
	if config.InstallationID != m.setupInstallationID {
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

// Preparation is outside the manager mutex and all database transactions. A
// rejected candidate cannot retire the current generation or its node lanes.
func (m *runtimeManager) prepareCandidate(ctx context.Context, input sandbox.Selection) (preparedRuntimeDeployment, error) {
	setup, err := m.deploymentService.SetupForSelection(m.setupInstallationID, input)
	if err != nil {
		return preparedRuntimeDeployment{}, err
	}
	candidate, err := m.prepareDeployment(ctx, setup)
	if err != nil {
		return preparedRuntimeDeployment{}, err
	}
	config := candidate.Config
	if config == nil || config.InstallationID != setup.InstallationID || config.ProviderKind != setup.Provider || config.Mode != setup.Mode || config.BackendFingerprint != setup.BackendFingerprint {
		return preparedRuntimeDeployment{}, sandbox.ErrInvalid
	}
	copied, err := validatedRuntimeProvider(config, m.registry)
	if err != nil {
		return preparedRuntimeDeployment{}, err
	}
	if err := ctx.Err(); err != nil {
		return preparedRuntimeDeployment{}, err
	}
	m.mu.Lock()
	closed := m.closed
	m.mu.Unlock()
	if closed {
		return preparedRuntimeDeployment{}, ErrExecutionUnavailable
	}
	if candidate.Selection.Provider != input.Provider {
		return preparedRuntimeDeployment{}, sandbox.ErrInvalid
	}
	candidate.Selection.ExpectedGeneration = input.ExpectedGeneration
	candidate.Config = &copied
	return candidate, nil
}

// The deployment commit is the point of no return. Publishing a validated candidate
// is infallible, including when shutdown or request cancellation follows commit.
func (m *runtimeManager) publishDeployment(candidate preparedRuntimeDeployment, committed deployment.View) {
	m.mu.Lock()
	defer m.mu.Unlock()
	config := *candidate.Config
	config.Generation = committed.Generation
	if m.switching {
		m.nodes = make(map[string]*runtimeNode)
	}
	m.config = config
	m.publishSelection(config.Generation, &config)
	m.switching = false
	m.switchDrained = nil
}
