package execution

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/gateway"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox/node"
	"github.com/google/uuid"
)

func TestDeferredSandboxDeploymentLoadsOnceBeforeNodeCreation(t *testing.T) {
	hub := node.NewHub(node.HubOptions{})
	defer hub.Close()
	id := uuid.NewString()
	var selected atomic.Bool
	var loads atomic.Int32
	configuration := &RuntimeProvider{InstallationID: id, ProviderKind: "docker", CoreURL: "https://core.example/api/v1", BackendFingerprint: strings.Repeat("a", 64), Provider: hub.Proxy(uuid.NewString(), "docker")}
	m, err := newRuntimeManager(nil, gateway.NewRegistry(), NewDeferredRuntimeProvider(id, func(context.Context) (*RuntimeProvider, error) {
		loads.Add(1)
		if !selected.Load() {
			return nil, nil
		}
		return configuration, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.stop(); m.drain() })
	if ready, err := m.ensureDeployment(t.Context()); err != nil || ready {
		t.Fatal("empty setup became ready", ready, err)
	}
	if _, err := m.node("first"); !errors.Is(err, ErrExecutionUnavailable) {
		t.Fatal("unconfigured node created", err)
	}
	selected.Store(true)
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if ready, err := m.ensureDeployment(t.Context()); err != nil || !ready {
				t.Errorf("activation failed: %v %v", ready, err)
			}
		}()
	}
	wg.Wait()
	if loads.Load() != 2 {
		t.Fatal("configuration loaded again after selection", loads.Load())
	}
	configuration.CoreURL = "https://changed.example/api/v1"
	a, err := m.node("first")
	if err != nil || a.lifecycle.config.CoreURL != "https://core.example/api/v1" {
		t.Fatal("mutable config reached worker", err)
	}
	m.stop()
	if _, err := m.ensureDeployment(t.Context()); !errors.Is(err, ErrExecutionUnavailable) {
		t.Fatal("stopped manager activated", err)
	}
}

func TestDeferredSandboxDeploymentShutdownCancelsLoad(t *testing.T) {
	entered := make(chan struct{})
	m, err := newRuntimeManager(nil, gateway.NewRegistry(), NewDeferredRuntimeProvider(uuid.NewString(), func(ctx context.Context) (*RuntimeProvider, error) {
		close(entered)
		<-ctx.Done()
		return nil, ctx.Err()
	}))
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := m.ensureDeployment(t.Context()); done <- err }()
	<-entered
	m.stop()
	m.drain()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal("shutdown did not cancel setup load", err)
	}
}

func TestDeferredSandboxProviderFailureKeepsRecoveryAvailable(t *testing.T) {
	hub := node.NewHub(node.HubOptions{})
	defer hub.Close()
	id := uuid.NewString()
	available := false
	loadErr := ErrExecutionUnavailable
	configuration := &RuntimeProvider{InstallationID: id, ProviderKind: "docker", CoreURL: "https://core.example/api/v1", BackendFingerprint: strings.Repeat("a", 64), Provider: hub.Proxy(uuid.NewString(), "docker")}
	m, err := newRuntimeManager(nil, gateway.NewRegistry(), NewDeferredRuntimeProvider(id, func(context.Context) (*RuntimeProvider, error) {
		if !available {
			return nil, loadErr
		}
		return configuration, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.stop(); m.drain() })
	if nodes, err := m.syncNodes(t.Context()); err != nil || len(nodes) != 0 {
		t.Fatal("unavailable provider stopped the manager", err)
	}
	if _, err := m.node("node"); !errors.Is(err, ErrExecutionUnavailable) {
		t.Fatal("unavailable provider admitted execution", err)
	}
	loadErr = errors.New("storage failure")
	if _, err := m.ensureDeployment(t.Context()); !errors.Is(err, loadErr) {
		t.Fatal("provider recovery hid a storage failure", err)
	}
	available = true
	if ready, err := m.ensureDeployment(t.Context()); err != nil || !ready {
		t.Fatal("repaired provider did not activate", ready, err)
	}
}
