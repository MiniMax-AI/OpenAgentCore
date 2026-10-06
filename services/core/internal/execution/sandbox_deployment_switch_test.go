package execution

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimegateway"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/docker"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/node"
	"github.com/google/uuid"
)

func TestSandboxManagerSwitchDrainsBeforeDirectActivation(t *testing.T) {
	hub := node.NewHub(node.HubOptions{})
	defer hub.Close()
	id := uuid.NewString()
	config := &RuntimeProvider{InstallationID: id, ProviderKind: "docker", Mode: "nodes", Generation: 1, CoreURL: "https://core.example/api/v1", BackendFingerprint: strings.Repeat("a", 64), Provider: hub.Proxy(uuid.NewString(), "docker", docker.Operations(), 1)}
	m, err := newRuntimeManager(Owner{Lease: heldLease{}}, nil, nil, nil, runtimegateway.NewRegistry(), NewDeferredRuntimeProvider(id, func(context.Context) (*RuntimeProvider, error) { return config, nil }))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { m.stop(); m.drain() }()
	if _, err := m.ensureDeployment(t.Context()); err != nil {
		t.Fatal(err)
	}
	old, err := m.node(uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	_, finish, err := m.enter(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	paused := make(chan error, 1)
	go func() { paused <- m.pauseDeployment(t.Context()) }()
	select {
	case <-old.lifecycle.ctx.Done():
	case <-time.After(time.Second):
		finish()
		t.Fatal("old lifecycle not cancelled")
	}
	if _, err := m.node(uuid.NewString()); !errors.Is(err, errRuntimeTransition) {
		finish()
		t.Fatal("transition admitted new lifecycle", err)
	}
	select {
	case err := <-paused:
		finish()
		t.Fatal("switch skipped active caller", err)
	default:
	}
	finish()
	select {
	case err := <-paused:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("switch drain blocked")
	}
	config = &RuntimeProvider{InstallationID: id, ProviderKind: "e2b", Mode: "direct", Generation: 2, CoreURL: "https://core.example/api/v1", BackendFingerprint: strings.Repeat("b", 64), Provider: hub.Proxy(uuid.NewString(), "docker", docker.Operations(), 1)}
	if err := m.activateDeployment(t.Context(), deployment.View{InstallationID: id, Generation: 2, Mode: "direct", Provider: "e2b"}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.node(uuid.NewString()); !errors.Is(err, ErrExecutionUnavailable) {
		t.Fatal("cloud created physical lifecycle", err)
	}
	current, err := m.node("")
	if err != nil || current.lifecycle.config.Generation != 2 {
		t.Fatal("cloud lifecycle not activated", err)
	}
	if len(m.nodes) != 1 {
		t.Fatal("old lifecycle retained after activation")
	}
}

func TestSandboxManagerFailedActivationStaysPaused(t *testing.T) {
	id := uuid.NewString()
	m, err := newRuntimeManager(Owner{Lease: heldLease{}}, nil, nil, nil, runtimegateway.NewRegistry(), NewDeferredRuntimeProvider(id, func(context.Context) (*RuntimeProvider, error) { return nil, errors.New("provider unavailable") }))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { m.stop(); m.drain() }()
	if err := m.pauseDeployment(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := m.activateDeployment(t.Context(), deployment.View{InstallationID: id, Generation: 2, Mode: "direct", Provider: "e2b"}); err == nil {
		t.Fatal("failed provider activated")
	}
	if _, _, err := m.enter(t.Context()); !errors.Is(err, errRuntimeTransition) {
		t.Fatal("failed activation reopened work", err)
	}
}

func TestSandboxManagerCancelledSwitchCannotResumeBeforeDrain(t *testing.T) {
	hub := node.NewHub(node.HubOptions{})
	defer hub.Close()
	id := uuid.NewString()
	config := &RuntimeProvider{InstallationID: id, ProviderKind: "docker", Mode: "nodes", Generation: 1, CoreURL: "https://core.example/api/v1", BackendFingerprint: strings.Repeat("a", 64), Provider: hub.Proxy(uuid.NewString(), "docker", docker.Operations(), 1)}
	m, err := newRuntimeManager(Owner{Lease: heldLease{}}, nil, nil, nil, runtimegateway.NewRegistry(), NewDeferredRuntimeProvider(id, func(context.Context) (*RuntimeProvider, error) { return config, nil }))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.ensureDeployment(t.Context()); err != nil {
		t.Fatal(err)
	}
	_, finish, err := m.enter(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	released := false
	defer func() {
		if !released {
			finish()
		}
		m.stop()
		m.drain()
	}()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	if err := m.pauseDeployment(ctx); !errors.Is(err, context.DeadlineExceeded) {
		cancel()
		t.Fatal("initial pause did not wait for old caller", err)
	}
	cancel()
	m.mu.Lock()
	originalDrain := m.switchDrained
	m.mu.Unlock()
	config = &RuntimeProvider{InstallationID: id, ProviderKind: "e2b", Mode: "direct", Generation: 2, CoreURL: "https://core.example/api/v1", BackendFingerprint: strings.Repeat("b", 64), Provider: hub.Proxy(uuid.NewString(), "docker", docker.Operations(), 1)}
	expected := deployment.View{InstallationID: id, Generation: 2, Mode: "direct", Provider: "e2b"}
	ctx, cancel = context.WithTimeout(t.Context(), 10*time.Millisecond)
	if err := m.activateDeployment(ctx, expected); !errors.Is(err, context.DeadlineExceeded) {
		cancel()
		t.Fatal("resume bypassed outstanding drain", err)
	}
	cancel()
	m.mu.Lock()
	sameDrain := m.switchDrained == originalDrain
	generation := m.config.Generation
	m.mu.Unlock()
	if !sameDrain || generation != 1 {
		t.Fatal("retry replaced drain or activated configuration")
	}
	finish()
	released = true
	if err := m.activateDeployment(t.Context(), expected); err != nil {
		t.Fatal("drained generation did not resume", err)
	}
}

func TestSandboxActivationCannotBypassOutstandingDrain(t *testing.T) {
	hub := node.NewHub(node.HubOptions{})
	defer hub.Close()
	id := uuid.NewString()
	m, err := newRuntimeManager(Owner{Lease: heldLease{}}, nil, nil, nil, runtimegateway.NewRegistry(), NewDeferredRuntimeProvider(id,
		func(context.Context) (*RuntimeProvider, error) { return nil, nil },
		func(_ context.Context, setup deployment.Setup) (PreparedRuntimeDeployment, error) {
			return PreparedRuntimeDeployment{Config: &RuntimeProvider{InstallationID: setup.InstallationID, ProviderKind: setup.Provider, Mode: setup.Mode, CoreURL: "https://core.example/api/v1", BackendFingerprint: setup.BackendFingerprint, Provider: hub.Proxy(uuid.NewString(), "docker", docker.Operations(), 1)}}, nil
		}))
	if err != nil {
		t.Fatal(err)
	}
	_, finish, err := m.enter(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { finish(); m.stop(); m.drain() }()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	if err := m.pauseDeployment(ctx); !errors.Is(err, context.DeadlineExceeded) {
		cancel()
		t.Fatal("pause did not retain an outstanding caller", err)
	}
	cancel()
	ctx, cancel = context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()
	err = m.activateDeployment(ctx, deployment.View{InstallationID: id, Generation: 1, Provider: "e2b", Mode: "direct"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("activation bypassed the unfinished drain", err)
	}
}
