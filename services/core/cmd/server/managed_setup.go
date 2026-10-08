package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/obs/log"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/execution"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/providercontract"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimeobs"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/node"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/providers"
)

// managedSetup publishes one immutable selection to execution, bootstrap and
// observation. The database owns the selection; this cache is never a writer.
type managedSetup struct {
	processPaths sandbox.ProcessPaths
	// registry builds the selected direct provider and discovers configuration;
	// the deployment setup reports what the registration declares.
	registry       *providers.Registry
	deployment     deploymentSetups
	allocations    generationAllocations
	hub            *node.Hub
	installationID string
	// sandboxLink is the Link every sandbox Serves on, empty when the origin
	// has no Link.
	sandboxLink   string
	selected      atomic.Pointer[managedSelection]
	providerCalls sandbox.CallFence
}

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

// generationAllocations pages the unreleased allocations whose generations
// still hold a credential. deployment.Reader implements it.
type generationAllocations interface {
	CredentialAllocations(context.Context, string) ([]deployment.Allocation, error)
}

// DiscoverConfiguration asks a Provider which configuration values its
// credential can use, with this installation's process paths.
func (s *managedSetup) DiscoverConfiguration(ctx context.Context, provider string, input sandbox.ConfigurationDiscoveryInput) (json.RawMessage, error) {
	return s.registry.DiscoverConfiguration(ctx, provider, input, s.processPaths)
}

// Empty selections retain their generation so a delayed provider load cannot
// republish a backend retired by reset.
type managedSelection struct {
	Generation uint64
	Config     *execution.RuntimeProvider
}

func (s *managedSetup) publishSelection(generation uint64, config *execution.RuntimeProvider) *execution.RuntimeProvider {
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
func (s *managedSetup) publish(config *execution.RuntimeProvider) {
	s.publishSelection(config.Generation, config)
}
func (s *managedSetup) publishUnconfigured(generation uint64) { s.publishSelection(generation, nil) }

func (s *managedSetup) load(ctx context.Context) (*execution.RuntimeProvider, error) {
	setup, err := s.deployment.Setup(ctx)
	if errors.Is(err, deployment.ErrCredentialUnreadable) {
		// A replaced credential key blocks hosted execution, not Core.
		log.Warn(ctx, "Hosted provider credential is unreadable; administrator recovery remains available", "error", err)
		return nil, fmt.Errorf("%w: %w", execution.ErrExecutionUnavailable, err)
	}
	if err != nil {
		return nil, err
	}
	if setup.InstallationID != s.installationID {
		return nil, errors.New("sandbox installation does not match setup")
	}
	if setup.Provider == "" {
		return s.publishSelection(setup.Generation, nil), nil
	}
	if selected := s.selected.Load(); selected != nil && selected.Generation >= setup.Generation {
		return selected.Config, nil
	}
	candidate, err := s.configuration(setup)
	if err == nil {
		candidate, err = s.routeGenerations(candidate, setup)
	}
	if err != nil {
		log.Warn(ctx, "Hosted provider is unavailable; administrator recovery remains available", "provider", setup.Provider, "error", err)
		return nil, fmt.Errorf("%w: %v", execution.ErrExecutionUnavailable, err)
	}
	return s.publishSelection(setup.Generation, candidate.Config), nil
}

// prepare validates a setup the deployment prepared for a selection, which has
// already rejected a provider whose guests cannot reach the public URL.
func (s *managedSetup) prepare(ctx context.Context, setup deployment.Setup) (execution.PreparedRuntimeDeployment, error) {
	candidate, err := s.configuration(setup)
	if err != nil {
		return execution.PreparedRuntimeDeployment{}, err
	}
	adapter, err := s.registry.Lookup(setup.Provider)
	if err != nil {
		return execution.PreparedRuntimeDeployment{}, err
	}
	direct := s.direct(setup)
	selection := direct.Selection
	if adapter.Configuration.Requirements().SelectionDiscovery.State == providercontract.Supported {
		if selection, err = s.registry.DiscoverSelection(ctx, direct); err != nil {
			return execution.PreparedRuntimeDeployment{}, err
		}
		setup.Specification, setup.Configuration = selection.DeploymentSpec, selection.Configuration
		if candidate, err = s.configuration(setup); err != nil {
			return execution.PreparedRuntimeDeployment{}, err
		}
	}
	candidate.Selection = &selection
	return s.routeGenerations(candidate, setup)
}

// Loading an already committed selection must retain provider access to its
// owned resources, even when a new-template validation would now fail.
func (s *managedSetup) configuration(setup deployment.Setup) (execution.PreparedRuntimeDeployment, error) {
	if setup.InstallationID != s.installationID {
		return execution.PreparedRuntimeDeployment{}, errors.New("sandbox installation does not match setup")
	}
	provider, err := s.provider(setup)
	if err != nil {
		return execution.PreparedRuntimeDeployment{}, fmt.Errorf("%w: %v", execution.ErrExecutionUnavailable, err)
	}
	selected := &execution.RuntimeProvider{InstallationID: setup.InstallationID, ProviderKind: setup.Provider, Generation: setup.Generation, Mode: setup.Mode,
		SandboxLink: s.sandboxLink, BackendFingerprint: setup.BackendFingerprint, Provider: provider}
	if setup.Suspension != nil {
		selected.Suspension = &execution.RuntimeSuspensionPolicy{IdleTimeout: time.Duration(setup.Suspension.IdleSeconds) * time.Second,
			Retention: time.Duration(setup.Suspension.RetentionSeconds) * time.Second}
	}
	return execution.PreparedRuntimeDeployment{Config: selected, Publish: s.publish}, nil
}

// observationSource returns the selected Provider and its registered kind for
// Runtime observation.
func (s *managedSetup) observationSource(ctx context.Context) (runtimeobs.Source, string, error) {
	selected, err := s.load(ctx)
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
func (s *managedSetup) provider(setup deployment.Setup) (sandbox.SandboxProvider, error) {
	if setup.Mode == "nodes" {
		if s.hub == nil {
			return nil, errors.New("sandbox node transport is unavailable")
		}
		return s.hub.GenerationProvider(setup.Operations, s.deployment.AllocationGeneration), nil
	}
	return s.registry.BuildDirect(s.direct(setup))
}

// direct is the setup's input to direct-mode construction and setup operations.
func (s *managedSetup) direct(setup deployment.Setup) sandbox.DirectConfig {
	return sandbox.DirectConfig{ProcessPaths: s.processPaths, InstallationID: setup.InstallationID, Selection: sandbox.Selection{Provider: setup.Provider, DeploymentSpec: setup.Specification, Configuration: setup.Configuration}, Fence: &s.providerCalls}
}
