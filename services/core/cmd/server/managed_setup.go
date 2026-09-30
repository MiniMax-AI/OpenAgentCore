package main

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/obs/log"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/execution"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/providercontract"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimeobs"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/node"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/providers"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
)

// managedSetup publishes one immutable selection to execution, bootstrap and
// observation. The database owns the selection; this cache is never a writer.
type managedSetup struct {
	processPaths sandbox.ProcessPaths
	store        interface {
		GetSandboxSetup(context.Context) (store.SandboxSetup, error)
		ResolveRuntimeGeneration(context.Context, sandbox.Reference) (string, uint64, error)
	}
	hub            *node.Hub
	installationID string
	// publicURL is OAC_PUBLIC_URL; every sandbox reaches Core through it.
	publicURL     string
	selected      atomic.Pointer[managedSelection]
	providerCalls sandbox.CallFence
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
	setup, err := s.store.GetSandboxSetup(ctx)
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

func (s *managedSetup) prepare(ctx context.Context, setup store.SandboxSetup) (execution.PreparedRuntimeDeployment, error) {
	// Adapters declare whether their guests require a public Core origin.
	requiresPublicOrigin, err := providers.RequiresPublicOrigin(setup.Provider)
	if err != nil {
		return execution.PreparedRuntimeDeployment{}, err
	}
	if requiresPublicOrigin && store.LoopbackOrigin(s.publicURL) {
		return execution.PreparedRuntimeDeployment{}, store.ErrSandboxPublicURLUnreachable
	}
	candidate, err := s.configuration(setup)
	if err != nil {
		return execution.PreparedRuntimeDeployment{}, err
	}
	selection := sandbox.Selection{Provider: setup.Provider, DeploymentSpec: setup.Specification, Configuration: setup.Configuration}
	if err := providercontract.Require(candidate.Config.Provider, "DiscoverSelection"); err == nil {
		discoverer := candidate.Config.Provider.(sandbox.SelectionDiscoverer)
		selection, err = discoverer.DiscoverSelection(ctx, selection)
		if err != nil {
			return execution.PreparedRuntimeDeployment{}, err
		}
		setup.Specification, setup.Configuration = selection.DeploymentSpec, selection.Configuration
		candidate, err = s.configuration(setup)
		if err != nil {
			return execution.PreparedRuntimeDeployment{}, err
		}
	} else if !errors.Is(err, providercontract.ErrUnsupported) {
		return execution.PreparedRuntimeDeployment{}, err
	}
	candidate.Selection = &selection
	return s.routeGenerations(candidate, setup)
}

// Loading an already committed selection must retain provider access to its
// owned resources, even when a new-template validation would now fail.
func (s *managedSetup) configuration(setup store.SandboxSetup) (execution.PreparedRuntimeDeployment, error) {
	if setup.InstallationID != s.installationID {
		return execution.PreparedRuntimeDeployment{}, errors.New("sandbox installation does not match setup")
	}
	provider, err := s.provider(setup)
	if err != nil {
		return execution.PreparedRuntimeDeployment{}, fmt.Errorf("%w: %v", execution.ErrExecutionUnavailable, err)
	}
	selected := &execution.RuntimeProvider{InstallationID: setup.InstallationID, ProviderKind: setup.Provider, Generation: setup.Generation, Mode: setup.Mode, AdmissionPaused: setup.AdmissionPaused,
		CoreURL: s.publicURL + "/api/v1", BackendFingerprint: setup.BackendFingerprint, Provider: provider}
	if sandbox.SupportsCheckpoint(provider) {
		selected.Suspension = &execution.RuntimeSuspensionPolicy{IdleTimeout: time.Duration(setup.IdleSeconds) * time.Second,
			Retention: time.Duration(setup.RetentionSeconds) * time.Second, MaxActive: 4, MaxRetained: 16}
	}
	return execution.PreparedRuntimeDeployment{Config: selected, Publish: s.publish}, nil
}

func (*managedSetup) ProviderOperations() providercontract.Operations {
	return providercontract.Operations{"ResolveObservationSource": {State: providercontract.Supported}}
}

func (s *managedSetup) ResolveObservationSource(ctx context.Context) (runtimeobs.Source, error) {
	selected, err := s.load(ctx)
	if err != nil {
		return nil, err
	}
	if selected == nil {
		return nil, runtimeobs.ErrUnavailable
	}
	source, ok := selected.Provider.(runtimeobs.Source)
	if !ok {
		return nil, providercontract.ErrContract
	}
	return source, nil
}

func (s *managedSetup) provider(setup store.SandboxSetup) (sandbox.SandboxProvider, error) {
	adapter, err := providers.Lookup(setup.Provider)
	if err != nil {
		return nil, err
	}
	if adapter.Mode == "nodes" {
		if s.hub == nil {
			return nil, errors.New("sandbox node transport is unavailable")
		}
		return s.hub.GenerationProvider(setup.Provider, s.store.ResolveRuntimeGeneration), nil
	}
	return providers.BuildDirect(providers.DirectConfig{ProcessPaths: s.processPaths, InstallationID: setup.InstallationID, Selection: sandbox.Selection{Provider: setup.Provider, DeploymentSpec: setup.Specification, Configuration: setup.Configuration}, Fence: &s.providerCalls})
}
