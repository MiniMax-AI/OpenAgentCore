package main

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/execution"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/runtimeobs"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox/node"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

// managedSetup publishes one immutable selection to execution, bootstrap and
// observation. The database owns the selection; this cache is never a writer.
type managedSetup struct {
	store          *store.Store
	hub            *node.Hub
	installationID string
	selected       atomic.Pointer[execution.RuntimeProvider]
}

func (s *managedSetup) load(ctx context.Context) (*execution.RuntimeProvider, error) {
	if selected := s.selected.Load(); selected != nil {
		return selected, nil
	}
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
	provider := s.hub.Provider(setup.Provider, func(ctx context.Context, ref sandbox.Reference) (string, error) {
		return s.store.ResolveRuntimeNode(ctx, ref.TenantID, ref.EnvironmentID)
	})
	selected := &execution.RuntimeProvider{InstallationID: setup.InstallationID, ProviderKind: setup.Provider,
		CoreURL: setup.CoreURL + "/api/v1", BackendFingerprint: setup.BackendFingerprint, Provider: provider}
	if setup.Provider == "microsandbox" {
		selected.Suspension = &execution.RuntimeSuspensionPolicy{IdleTimeout: time.Duration(setup.IdleSeconds) * time.Second,
			Retention: time.Duration(setup.RetentionSeconds) * time.Second, MaxActive: 4, MaxRetained: 16}
	}
	s.selected.CompareAndSwap(nil, selected)
	return s.selected.Load(), nil
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
