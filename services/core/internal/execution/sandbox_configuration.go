package execution

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/obs/log"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/providercontract"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimeobs"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/providers"
)

// deploymentSetups reads the committed deployment setup and its retained
// generations. *deployment.Service implements it.
type deploymentSetups interface {
	Setup(context.Context) (deployment.Setup, error)
	AllocationSetup(context.Context, sandbox.Reference) (deployment.Setup, error)
	GenerationPage(context.Context, int64) ([]deployment.Setup, error)
	WithCredential(owner, candidate deployment.Setup) (deployment.Setup, error)
	// AllocationGeneration resolves the node and generation that own an
	// allocation.
	AllocationGeneration(context.Context, sandbox.Reference) (string, uint64, error)
}

// ProviderRegistry is the setup and direct-construction dependency of execution.
// The composition root supplies the registered providers; support stays declared
// by their ConfigurationAdapter and SandboxProvider contracts.
type ProviderRegistry interface {
	Lookup(string) (providers.Adapter, error)
	BuildDirect(sandbox.DirectConfig) (sandbox.SandboxProvider, error)
	DiscoverConfiguration(context.Context, string, sandbox.ConfigurationDiscoveryInput, sandbox.ProcessPaths) (json.RawMessage, error)
	DiscoverSelection(context.Context, sandbox.DirectConfig) (sandbox.Selection, error)
	VerifyCredential(context.Context, sandbox.DirectConfig, []sandbox.Reference) error
}

// NodeProviders binds allocation-owned node and generation identities to the
// node transport. The server supplies its node.Hub.
type NodeProviders interface {
	Proxy(string, providercontract.Operations, uint64) sandbox.SandboxProvider
}

// DiscoverConfiguration asks a Provider which configuration values its
// credential can use, with this installation's process paths.
func (w *Worker) DiscoverConfiguration(ctx context.Context, provider string, input sandbox.ConfigurationDiscoveryInput) (json.RawMessage, error) {
	return w.runtimes.providers.DiscoverConfiguration(ctx, provider, input, w.runtimes.processPaths)
}

// Empty selections retain their generation so a delayed provider load cannot
// republish a backend retired by reset.
type managedSelection struct {
	Generation uint64
	Config     *RuntimeProvider
}

func (s *runtimeManager) publishSelection(generation uint64, config *RuntimeProvider) *RuntimeProvider {
	next := &managedSelection{Generation: generation, Config: config}
	for {
		current := s.selected.Load()
		if current != nil && current.Generation >= generation {
			return current.Config
		}
		if s.selected.CompareAndSwap(current, next) {
			return config
		}
	}
}

func (s *runtimeManager) loadDeployment(ctx context.Context) (*RuntimeProvider, error) {
	setup, err := s.setups.Setup(ctx)
	if errors.Is(err, deployment.ErrCredentialUnreadable) {
		// A replaced credential key blocks hosted execution, not Core.
		log.Warn(ctx, "Hosted provider credential is unreadable; administrator recovery remains available", "error", err)
		return nil, fmt.Errorf("%w: %w", ErrExecutionUnavailable, err)
	}
	if err != nil {
		return nil, err
	}
	if setup.InstallationID != s.setupInstallationID {
		return nil, errors.New("sandbox installation does not match setup")
	}
	if setup.Provider == "" {
		return s.publishSelection(setup.Generation, nil), nil
	}
	if selected := s.selected.Load(); selected != nil && selected.Generation >= setup.Generation {
		return selected.Config, nil
	}
	candidate, err := s.configuration(setup)
	if err != nil {
		log.Warn(ctx, "Hosted provider is unavailable; administrator recovery remains available", "provider", setup.Provider, "error", err)
		return nil, fmt.Errorf("%w: %v", ErrExecutionUnavailable, err)
	}
	return s.publishSelection(setup.Generation, candidate.Config), nil
}

// prepare validates a setup the deployment prepared for a selection, which has
// already rejected a provider whose guests cannot reach the public URL.
func (s *runtimeManager) prepareDeployment(ctx context.Context, setup deployment.Setup) (preparedRuntimeDeployment, error) {
	candidate, err := s.configuration(setup)
	if err != nil {
		return preparedRuntimeDeployment{}, err
	}
	adapter, err := s.providers.Lookup(setup.Provider)
	if err != nil {
		return preparedRuntimeDeployment{}, err
	}
	direct := s.direct(setup)
	selection := direct.Selection
	if adapter.Configuration.Requirements().SelectionDiscovery.State == providercontract.Supported {
		if selection, err = s.providers.DiscoverSelection(ctx, direct); err != nil {
			return preparedRuntimeDeployment{}, err
		}
		setup.Specification, setup.Configuration = selection.DeploymentSpec, selection.Configuration
		if candidate, err = s.configuration(setup); err != nil {
			return preparedRuntimeDeployment{}, err
		}
	}
	candidate.Selection = selection
	candidate.Setup = setup
	return candidate, nil
}

// Loading an already committed selection must retain provider access to its
// owned resources, even when a new-template validation would now fail.
func (s *runtimeManager) configuration(setup deployment.Setup) (preparedRuntimeDeployment, error) {
	if setup.InstallationID != s.setupInstallationID {
		return preparedRuntimeDeployment{}, errors.New("sandbox installation does not match setup")
	}
	provider, err := s.provider(setup)
	if err != nil {
		return preparedRuntimeDeployment{}, fmt.Errorf("%w: %v", ErrExecutionUnavailable, err)
	}
	selected := &RuntimeProvider{InstallationID: setup.InstallationID, ProviderKind: setup.Provider, Generation: setup.Generation, Mode: setup.Mode,
		SandboxLink: s.sandboxLink, BackendFingerprint: setup.BackendFingerprint, Provider: provider}
	if setup.Suspension != nil {
		selected.Suspension = &RuntimeSuspensionPolicy{IdleTimeout: time.Duration(setup.Suspension.IdleSeconds) * time.Second,
			Retention: time.Duration(setup.Suspension.RetentionSeconds) * time.Second}
	}
	return preparedRuntimeDeployment{Config: selected, Setup: setup}, nil
}

// ObservationSource returns the selected Provider and its registered kind for
// Runtime observation.
func (w *Worker) ObservationSource(ctx context.Context) (runtimeobs.Source, string, error) {
	selected, err := w.runtimes.loadDeployment(ctx)
	if err != nil {
		return nil, "", err
	}
	if selected == nil {
		return nil, "", runtimeobs.ErrUnavailable
	}
	return selected.Provider, selected.ProviderKind, nil
}

// provider builds the setup's provider. The setup carries the mode and
// declared operations that deployment read from the provider's registration.
func (s *runtimeManager) provider(setup deployment.Setup) (sandbox.SandboxProvider, error) {
	if setup.Mode == "nodes" {
		if s.nodeProviders == nil {
			return nil, errors.New("sandbox node transport is unavailable")
		}
		return sandbox.NewGenerationRouter(setup.Operations, func(ctx context.Context, ref sandbox.Reference) (sandbox.SandboxProvider, func(), error) {
			id, generation, err := s.setups.AllocationGeneration(ctx, ref)
			if err != nil {
				return nil, nil, err
			}
			return s.nodeProviders.Proxy(id, setup.Operations, generation), func() {}, nil
		}), nil
	}
	provider, err := s.providers.BuildDirect(s.direct(setup))
	if err != nil {
		return nil, err
	}
	routed := sandbox.NewGenerationRouter(provider.ProviderOperations(), s.directProvider)
	if err := sandbox.ValidateProvider(routed); err != nil {
		return nil, err
	}
	return routed, nil
}

// direct is the setup's input to direct-mode construction and setup operations.
func (s *runtimeManager) direct(setup deployment.Setup) sandbox.DirectConfig {
	return sandbox.DirectConfig{ProcessPaths: s.processPaths, InstallationID: setup.InstallationID, Selection: sandbox.Selection{Provider: setup.Provider, DeploymentSpec: setup.Specification, Configuration: setup.Configuration}, Fence: &s.providerCalls}
}
