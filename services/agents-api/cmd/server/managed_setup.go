package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync/atomic"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/obs/log"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/execution"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/runtimeobs"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox/e2b"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox/node"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

// managedSetup publishes one immutable selection to execution, bootstrap and
// observation. The database owns the selection; this cache is never a writer.
type managedSetup struct {
	store interface {
		GetSandboxSetup(context.Context) (store.SandboxSetup, error)
		ResolveRuntimeNode(context.Context, string, string) (string, error)
	}
	hub            *node.Hub
	installationID string
	selected       atomic.Pointer[execution.RuntimeProvider]
}

func (s *managedSetup) load(ctx context.Context) (*execution.RuntimeProvider, error) {
	setup, err := s.store.GetSandboxSetup(ctx)
	if err != nil {
		return nil, err
	}
	if setup.InstallationID != s.installationID {
		return nil, errors.New("sandbox installation does not match setup")
	}
	if setup.Provider == "" {
		return nil, nil
	}
	if selected := s.selected.Load(); selected != nil && selected.Generation == setup.Generation {
		return selected, nil
	}
	candidate, err := s.configuration(setup)
	if err != nil {
		log.Warn(ctx, "Hosted provider is unavailable; administrator recovery remains available", "provider", setup.Provider, "error", err)
		return nil, fmt.Errorf("%w: %v", execution.ErrExecutionUnavailable, err)
	}
	// A slower read cannot replace a generation that committed while this
	// configuration was loading. Publication performs no external work.
	for {
		selected := s.selected.Load()
		if selected != nil && selected.Generation >= setup.Generation {
			return selected, nil
		}
		if s.selected.CompareAndSwap(selected, candidate.Config) {
			return candidate.Config, nil
		}
	}
}

func (s *managedSetup) prepare(ctx context.Context, setup store.SandboxSetup) (execution.PreparedRuntimeDeployment, error) {
	candidate, err := s.configuration(setup)
	if err != nil {
		return execution.PreparedRuntimeDeployment{}, err
	}
	if provider, ok := candidate.Config.Provider.(*e2b.Provider); ok {
		if err := provider.ValidateDeployment(ctx); err != nil {
			if errors.Is(err, sandbox.ErrInvalid) {
				return execution.PreparedRuntimeDeployment{}, &store.SandboxConfigurationError{Message: "E2B configuration was rejected; select a ready fixed template build whose CPU and memory match the deployment specification"}
			}
			return execution.PreparedRuntimeDeployment{}, fmt.Errorf("%w: E2B validation could not be confirmed; verify the helper, credential, network and fixed template build before retrying", execution.ErrExecutionUnavailable)
		}
	}
	return candidate, nil
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
	selected := &execution.RuntimeProvider{InstallationID: setup.InstallationID, ProviderKind: setup.Provider, Generation: setup.Generation, Mode: setup.Mode, Maintenance: setup.Maintenance,
		CoreURL: setup.CoreURL + "/api/v1", BackendFingerprint: setup.BackendFingerprint, Provider: provider}
	if setup.Provider == "microsandbox" {
		selected.Suspension = &execution.RuntimeSuspensionPolicy{IdleTimeout: time.Duration(setup.IdleSeconds) * time.Second,
			Retention: time.Duration(setup.RetentionSeconds) * time.Second, MaxActive: 4, MaxRetained: 16}
	}
	return execution.PreparedRuntimeDeployment{Config: selected, Publish: s.selected.Store}, nil
}

func (s *managedSetup) webSocketURL(fallback string) func(context.Context) (string, error) {
	return func(ctx context.Context) (string, error) {
		selected, err := s.load(ctx)
		if err != nil {
			return "", err
		}
		if selected == nil {
			return fallback, nil
		}
		return runtimeWebSocketURL(selected.CoreURL)
	}
}

func (s *managedSetup) ObservationProviderType() string {
	if selected := s.selected.Load(); selected != nil {
		return selected.ProviderKind
	}
	return ""
}

func (s *managedSetup) Observe(ctx context.Context, target runtimeobs.Target) (runtimeobs.Sample, error) {
	selected, err := s.load(ctx)
	if err != nil {
		return runtimeobs.Sample{}, err
	}
	if selected == nil {
		return runtimeobs.Sample{}, runtimeobs.ErrUnavailable
	}
	source, ok := selected.Provider.(runtimeobs.Source)
	if !ok {
		return runtimeobs.Sample{}, runtimeobs.ErrUnavailable
	}
	return source.Observe(ctx, target)
}

func (s *managedSetup) provider(setup store.SandboxSetup) (sandbox.Provider, error) {
	switch setup.Provider {
	case "docker", "microsandbox":
		if s.hub == nil {
			return nil, errors.New("sandbox node transport is unavailable")
		}
		return s.hub.Provider(setup.Provider, func(ctx context.Context, ref sandbox.Reference) (string, error) {
			return s.store.ResolveRuntimeNode(ctx, ref.TenantID, ref.EnvironmentID)
		}), nil
	case "e2b":
		if setup.E2B == nil {
			return nil, errors.New("E2B deployment configuration is unavailable")
		}
		binary := os.Getenv("AGENTS_API_E2B_PROVIDER_BIN")
		if binary == "" {
			binary = "/opt/parsar/e2b/agents-api-e2b-provider"
		}
		provider, err := e2b.New(e2b.Config{Binary: binary, StateDir: os.Getenv("AGENTS_API_E2B_STATE_DIR"),
			Resources: &setup.Specification.Resources, InstallationID: setup.InstallationID, APIKey: setup.E2B.APIKey, Template: setup.E2B.Template, TimeoutSeconds: 3600})
		if err != nil {
			return nil, errors.New("E2B provider cannot load; check the installed helper and private state directory")
		}
		return provider, nil
	default:
		return nil, errors.New("sandbox provider is unavailable")
	}
}
