package execution

import (
	"context"
	"errors"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

// PreparedRuntimeDeployment has completed provider validation without publishing
// a selection. Publish must only update in-memory state and must not fail.
// Selection is the resolved typed configuration returned by preparation.
type PreparedRuntimeDeployment struct {
	Config           *RuntimeProvider
	Publish          func(*RuntimeProvider)
	Selection        *sandbox.Selection
	VerifyCredential func(context.Context) error
	FenceCredential  func(context.Context) (func(), error)
}

type RuntimeDeploymentPreparer func(context.Context, store.SandboxSetup) (PreparedRuntimeDeployment, error)

// NewDeferredRuntimeProvider enables Web setup for one fixed installation. The
// loader returns nil until selection, then the committed immutable generation.
// Replacement is serialized by the deployment mutation gate and drain flow.
func NewDeferredRuntimeProvider(installationID string, load func(context.Context) (*RuntimeProvider, error), prepare ...RuntimeDeploymentPreparer) *RuntimeProvider {
	config := &RuntimeProvider{InstallationID: installationID, loadDeployment: load}
	if len(prepare) == 1 {
		config.prepareDeployment = prepare[0]
	}
	return config
}

func (w *Worker) InitializeSandboxDeployment(ctx context.Context, input store.SandboxDeploymentSetupRequest) (store.RuntimeDeploymentView, error) {
	unlock, err := w.runtimes.lockMutation(ctx)
	if err != nil {
		return store.RuntimeDeploymentView{}, err
	}
	defer unlock()
	m := w.runtimes
	if err := m.store.CheckSandboxDeploymentSetup(ctx, m.setupInstallationID, input); err != nil {
		return store.RuntimeDeploymentView{}, err
	}
	candidate, err := m.prepareCandidate(ctx, input)
	if err != nil {
		return store.RuntimeDeploymentView{}, err
	}
	// An idempotent setup retry can arrive after an interrupted replacement.
	// It must not reopen admission while that replacement is still draining.
	m.mu.Lock()
	switching := m.switching
	m.mu.Unlock()
	if switching {
		if err := m.pauseDeployment(ctx); err != nil {
			return store.RuntimeDeploymentView{}, err
		}
	}
	result, err := m.store.InitializeSandboxDeployment(ctx, m.setupInstallationID, *candidate.Selection)
	if err != nil {
		return store.RuntimeDeploymentView{}, err
	}
	m.publishDeployment(candidate, result)
	return result, nil
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
	if config.InstallationID != m.setupInstallationID || config.LocalNodeID != "" || config.loadDeployment != nil || config.ProviderKind == "" {
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
func (m *runtimeManager) prepareCandidate(ctx context.Context, input store.SandboxDeploymentSetupRequest) (PreparedRuntimeDeployment, error) {
	if m.prepareDeployment == nil {
		return PreparedRuntimeDeployment{}, ErrExecutionUnavailable
	}
	setup, err := store.SandboxSetupForSelection(m.setupInstallationID, input)
	if err != nil {
		return PreparedRuntimeDeployment{}, err
	}
	candidate, err := m.prepareDeployment(ctx, setup)
	if err != nil {
		return PreparedRuntimeDeployment{}, err
	}
	config := candidate.Config
	if config == nil || config.InstallationID != setup.InstallationID || config.ProviderKind != setup.Provider || config.Mode != setup.Mode || config.CoreURL == "" || config.BackendFingerprint != setup.BackendFingerprint || config.LocalNodeID != "" || config.loadDeployment != nil || config.prepareDeployment != nil {
		return PreparedRuntimeDeployment{}, sandbox.ErrInvalid
	}
	copied, err := validatedRuntimeProvider(config, m.registry)
	if err != nil {
		return PreparedRuntimeDeployment{}, err
	}
	if err := ctx.Err(); err != nil {
		return PreparedRuntimeDeployment{}, err
	}
	m.mu.Lock()
	closed := m.closed
	m.mu.Unlock()
	if closed {
		return PreparedRuntimeDeployment{}, ErrExecutionUnavailable
	}
	if candidate.Selection == nil {
		candidate.Selection = &input
	}
	if candidate.Selection.Provider != input.Provider {
		return PreparedRuntimeDeployment{}, sandbox.ErrInvalid
	}
	candidate.Selection.ExpectedGeneration = input.ExpectedGeneration
	candidate.Config = &copied
	return candidate, nil
}

// The store commit is the point of no return. Publishing a validated candidate
// is infallible, including when shutdown or request cancellation follows commit.
func (m *runtimeManager) publishDeployment(candidate PreparedRuntimeDeployment, committed store.RuntimeDeploymentView) {
	m.mu.Lock()
	defer m.mu.Unlock()
	config := *candidate.Config
	config.Generation, config.AdmissionPaused = committed.Generation, committed.Reset != nil
	if m.switching {
		m.nodes = make(map[string]*runtimeNode)
	}
	m.config = config
	if candidate.Publish != nil {
		candidate.Publish(&config)
	}
	m.switching = false
	m.switchDrained = nil
}
