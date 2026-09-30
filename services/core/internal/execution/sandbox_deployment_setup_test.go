package execution

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimegateway"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/docker"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/e2b"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/node"
	"github.com/google/uuid"
)

func TestDeferredSandboxDeploymentLoadsOnceBeforeNodeCreation(t *testing.T) {
	hub := node.NewHub(node.HubOptions{})
	defer hub.Close()
	id := uuid.NewString()
	var selected atomic.Bool
	var loads atomic.Int32
	configuration := &RuntimeProvider{InstallationID: id, ProviderKind: "docker", CoreURL: "https://core.example/api/v1", BackendFingerprint: strings.Repeat("a", 64), Provider: hub.Proxy(uuid.NewString(), "docker", docker.Operations(), 1)}
	m, err := newRuntimeManager(Owner{Lease: heldLease{}}, nil, runtimegateway.NewRegistry(), NewDeferredRuntimeProvider(id, func(context.Context) (*RuntimeProvider, error) {
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
	m, err := newRuntimeManager(Owner{Lease: heldLease{}}, nil, runtimegateway.NewRegistry(), NewDeferredRuntimeProvider(uuid.NewString(), func(ctx context.Context) (*RuntimeProvider, error) {
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
	configuration := &RuntimeProvider{InstallationID: id, ProviderKind: "docker", CoreURL: "https://core.example/api/v1", BackendFingerprint: strings.Repeat("a", 64), Provider: hub.Proxy(uuid.NewString(), "docker", docker.Operations(), 1)}
	m, err := newRuntimeManager(Owner{Lease: heldLease{}}, nil, runtimegateway.NewRegistry(), NewDeferredRuntimeProvider(id, func(context.Context) (*RuntimeProvider, error) {
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

func TestRejectedSandboxCandidatePreservesActiveGeneration(t *testing.T) {
	hub := node.NewHub(node.HubOptions{})
	defer hub.Close()
	id := uuid.NewString()
	config := &RuntimeProvider{InstallationID: id, ProviderKind: "docker", Mode: "nodes", Generation: 1, CoreURL: "https://core.example/api/v1", BackendFingerprint: strings.Repeat("a", 64), Provider: hub.Proxy(uuid.NewString(), "docker", docker.Operations(), 1)}
	rejected := errors.New("candidate provider unavailable")
	m, err := newRuntimeManager(Owner{Lease: heldLease{}}, unitDeploymentService(t), runtimegateway.NewRegistry(), NewDeferredRuntimeProvider(id, func(context.Context) (*RuntimeProvider, error) { return config, nil },
		func(context.Context, deployment.Setup) (PreparedRuntimeDeployment, error) {
			return PreparedRuntimeDeployment{}, rejected
		}))
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
	input := sandbox.Selection{Provider: "e2b", Configuration: &e2b.DeploymentConfiguration{APIKey: "fixture-key", Template: "runtime:" + uuid.NewString()}, DeploymentSpec: sandbox.DeploymentSpec{Resources: sandbox.Resources{CPUs: 2, MemoryMiB: 1024}}}
	if _, err := m.prepareCandidate(t.Context(), input); !errors.Is(err, rejected) {
		t.Fatal("candidate rejection was lost", err)
	}
	if m.config.Generation != 1 || m.config.Provider != config.Provider || m.switching || old.lifecycle.ctx.Err() != nil {
		t.Fatal("rejected candidate replaced or drained the active configuration")
	}
	_, finish, err := m.enter(t.Context())
	if err != nil {
		t.Fatal("candidate rejection stopped current execution", err)
	}
	finish()
}

func TestSandboxCandidateValidationDoesNotHoldManagerLock(t *testing.T) {
	id := uuid.NewString()
	entered, release := make(chan struct{}), make(chan struct{})
	m, err := newRuntimeManager(Owner{Lease: heldLease{}}, unitDeploymentService(t), runtimegateway.NewRegistry(), NewDeferredRuntimeProvider(id, func(context.Context) (*RuntimeProvider, error) { return nil, nil },
		func(context.Context, deployment.Setup) (PreparedRuntimeDeployment, error) {
			close(entered)
			<-release
			return PreparedRuntimeDeployment{}, errors.New("rejected")
		}))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { m.stop(); m.drain() }()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = m.prepareCandidate(t.Context(), sandbox.Selection{Provider: "e2b", Configuration: &e2b.DeploymentConfiguration{APIKey: "fixture-key", Template: "runtime:" + uuid.NewString()}, DeploymentSpec: sandbox.DeploymentSpec{Resources: sandbox.Resources{CPUs: 2, MemoryMiB: 1024}}})
	}()
	<-entered
	stopped := make(chan struct{})
	go func() { m.stop(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		close(release)
		<-done
		t.Fatal("provider validation blocked manager shutdown")
	}
	close(release)
	<-done
}

func TestCommittedSandboxCandidatePublishesAfterShutdown(t *testing.T) {
	hub := node.NewHub(node.HubOptions{})
	defer hub.Close()
	id := uuid.NewString()
	m, err := newRuntimeManager(Owner{Lease: heldLease{}}, nil, runtimegateway.NewRegistry(), NewDeferredRuntimeProvider(id, func(context.Context) (*RuntimeProvider, error) { return nil, nil }))
	if err != nil {
		t.Fatal(err)
	}
	if err := m.pauseDeployment(t.Context()); err != nil {
		t.Fatal(err)
	}
	config := &RuntimeProvider{InstallationID: id, ProviderKind: "e2b", Mode: "direct", CoreURL: "https://core.example/api/v1", BackendFingerprint: strings.Repeat("b", 64), Provider: hub.Proxy(uuid.NewString(), "docker", docker.Operations(), 1)}
	var published *RuntimeProvider
	candidate := PreparedRuntimeDeployment{Config: config, Publish: func(value *RuntimeProvider) { published = value }}
	m.stop()
	m.publishDeployment(candidate, deployment.View{InstallationID: id, Generation: 2, Mode: "direct", Provider: "e2b", Reset: &deployment.Reset{}})
	m.drain()
	if m.config.Generation != 2 || !m.config.AdmissionPaused || published == nil || published.Generation != 2 || !published.AdmissionPaused || m.switching {
		t.Fatal("committed candidate was lost during shutdown")
	}
	if _, _, err := m.enter(t.Context()); !errors.Is(err, ErrExecutionUnavailable) {
		t.Fatal("publication reopened a closed manager", err)
	}
}
