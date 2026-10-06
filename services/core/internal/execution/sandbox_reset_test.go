package execution

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/adminaudit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/deploymentpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgtest"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/sessionpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimegateway"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/docker"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/e2b"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/node"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The execution lease is database-scoped, so these manager tests own a database.
// They receive the pooled Store, the Owner of its execution lease, which the
// test closes when it ends, and the pooled deployment service and reader the
// Worker receives beside it.
func resetManagerStore(t *testing.T) (*store.Store, Owner, *deployment.Service, deployment.Reader) {
	t.Helper()
	return resetManagerStoreConfig(t, nil)
}

func resetManagerStoreConfig(t *testing.T, configure func(*pgxpool.Config)) (*store.Store, Owner, *deployment.Service, deployment.Reader) {
	t.Helper()
	s, owner, deployments, reader, _ := resetManagerStoreDB(t, configure)
	return s, owner, deployments, reader
}

// resetManagerStoreDB also returns the test database, for tests that build
// adapters on it.
func resetManagerStoreDB(t *testing.T, configure func(*pgxpool.Config)) (*store.Store, Owner, *deployment.Service, deployment.Reader, *pgxpool.Pool) {
	t.Helper()
	pool := pgtest.OpenIsolated(t, configure)
	cipher := testCredentialCipher(t)
	s := store.NewWithCredentialCipher(pool, cipher)
	s.SetPlacement(fixtureRules(t))
	owner, deployments, reader := testOwner(t, pool, cipher, s)
	return s, owner, deployments, reader, pool
}

// testCredentialCipher is the credential key of the Store resetManagerStoreDB
// builds, for adapters built beside it.
func testCredentialCipher(t *testing.T) *credentialcrypto.Cipher {
	t.Helper()
	cipher, err := credentialcrypto.New(bytes.Repeat([]byte{8}, 32))
	if err != nil {
		t.Fatal(err)
	}
	return cipher
}

// testOwner acquires the execution lease on pool and builds s's execution
// writer and the deployment and Session execution operations on it, and the pooled
// deployment service and reader, as cmd/server does. The lease closes when the
// test ends.
func testOwner(t *testing.T, pool *pgxpool.Pool, cipher *credentialcrypto.Cipher, s *store.Store) (Owner, *deployment.Service, deployment.Reader) {
	t.Helper()
	lease, err := pgunit.AcquireLease(t.Context(), pool)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lease.Close(context.Background()) })
	deployments, reader, operations := testDeployment(t, pool, cipher, lease)
	return Owner{Lease: lease, Store: store.NewExecution(s, lease), Deployment: operations, Sessions: sessionExecution(t, lease)}, deployments, reader
}

// sessionExecution builds the Session execution operations on lease, as
// cmd/server does.
func sessionExecution(t *testing.T, lease *pgunit.Lease) *sessions.ExecutionOperations {
	t.Helper()
	operations, err := sessions.NewExecutionOperations(sessionpg.NewExecution(lease))
	if err != nil {
		t.Fatal(err)
	}
	return operations
}

func TestSandboxResetPageTimeoutRecoversCommittedOwner(t *testing.T) {
	_, owner, deployments, reader := resetManagerStore(t)
	id := initializeE2BDeployment(t, owner)
	hub := node.NewHub(node.HubOptions{})
	defer hub.Close()
	loads := 0
	config := NewDeferredRuntimeProvider(id, func(ctx context.Context) (*RuntimeProvider, error) {
		if ctx.Err() != nil {
			t.Error("recovery inherited cancelled page")
		}
		loads++
		setup, err := deployments.Setup(ctx)
		if err != nil || setup.Provider == "" {
			return nil, err
		}
		return &RuntimeProvider{InstallationID: id, ProviderKind: setup.Provider, Mode: setup.Mode, Generation: setup.Generation, CoreURL: "https://core.example/api/v1", BackendFingerprint: setup.BackendFingerprint, Provider: hub.Proxy(uuid.NewString(), "docker", docker.Operations(), 1)}, nil
	})
	m, err := newRuntimeManager(owner, deployments, reader, nil, runtimegateway.NewRegistry(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { m.stop(); m.drain() }()
	if _, err := m.ensureDeployment(t.Context()); err != nil {
		t.Fatal(err)
	}
	old, err := m.node("")
	if err != nil {
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
	}()
	audit := adminaudit.WithSource(t.Context(), adminaudit.Source{CredentialID: "reset-test", ActorLabel: "operator", RequestID: "request", TraceID: "trace"})
	if err := owner.Deployment.StartReset(audit, id, deployment.ResetRequest{ExpectedGeneration: 1, Clear: deployment.ResetForce}); err != nil {
		t.Fatal(err)
	}
	reset, err := deployments.View(t.Context())
	if err != nil || reset.Reset == nil {
		t.Fatal("reset did not start", reset, err)
	}
	page, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- m.resetPage(t.Context(), page) }()
	select {
	case <-old.lifecycle.ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("reset never reached drain")
	}
	cancel() // Expire this page only after its drain barrier is established.
	finish()
	released = true
	select {
	case err := <-done:
		if err != nil {
			t.Fatal("recoverable page cancellation stopped owner", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("owner recovery blocked")
	}
	current, err := deployments.View(t.Context())
	if err != nil || current.Reset == nil || current.Generation != 1 || !current.Reset.RequestedAt.Equal(reset.Reset.RequestedAt) {
		t.Fatal("page timeout lost durable reset", current, err)
	}
	if loads < 2 {
		t.Fatal("committed provider was not restored")
	}
	_, finishNext, err := m.enter(t.Context())
	if err != nil {
		t.Fatal("recovery left barrier closed", err)
	}
	finishNext()
	if err := m.resetStep(t.Context()); err != nil {
		t.Fatal(err)
	}
	current, err = deployments.View(t.Context())
	if err != nil || current.Reset != nil || current.Provider != "" || current.Generation != 2 {
		t.Fatal("next tick did not finish", current, err)
	}
}

// initializeE2BDeployment claims a new installation for Web setup and selects
// E2B at generation 1 through owner's deployment execution operations.
func initializeE2BDeployment(t *testing.T, owner Owner) string {
	t.Helper()
	id := uuid.NewString()
	if err := owner.Deployment.Claim(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	selection := sandbox.Selection{Provider: "e2b", Configuration: &e2b.DeploymentConfiguration{APIKey: "fixture-key", Template: "runtime:" + uuid.NewString()}}
	selection.Resources.CPUs = 2
	selection.Resources.MemoryMiB = 2048
	if _, err := owner.Deployment.Initialize(t.Context(), id, selection); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestSandboxResetPublishesCommittedGenerationWithoutReading(t *testing.T) {
	_, owner, pooled, _, pool := resetManagerStoreDB(t, nil)
	id := initializeE2BDeployment(t, owner)
	// The manager reads the deployment through a reader that fails every read
	// of the committed reset, so publication cannot depend on one.
	adapter := deploymentpg.New(pgunit.NewPool(pool), nil)
	errCommittedRead := errors.New("committed deployment read failed")
	committedReads := 0
	reader := &strictDeploymentReader{t: t, snapshot: func(ctx context.Context) (deployment.Snapshot, error) {
		snapshot, err := adapter.Snapshot(ctx)
		if err == nil && snapshot.Record.Generation == 2 {
			committedReads++
			return deployment.Snapshot{}, errCommittedRead
		}
		return snapshot, err
	}}
	deployments, _ := deploymentOperations(t, &strictDeploymentStorage{t: t}, reader, &strictExecutionStorage{t: t})
	hub := node.NewHub(node.HubOptions{})
	defer hub.Close()
	config := NewDeferredRuntimeProvider(id, func(ctx context.Context) (*RuntimeProvider, error) {
		setup, err := pooled.Setup(ctx)
		if err != nil || setup.Provider == "" {
			return nil, err
		}
		return &RuntimeProvider{InstallationID: id, ProviderKind: setup.Provider, Mode: setup.Mode, Generation: setup.Generation, CoreURL: "https://core.example/api/v1", BackendFingerprint: setup.BackendFingerprint, Provider: hub.Proxy(uuid.NewString(), "docker", docker.Operations(), 1)}, nil
	})
	var published []uint64
	config.PublishUnconfigured = func(generation uint64) {
		if committedReads != 0 {
			t.Error("a deployment read stood between the reset commit and its publication")
		}
		published = append(published, generation)
	}
	m, err := newRuntimeManager(owner, deployments, adapter, nil, runtimegateway.NewRegistry(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { m.stop(); m.drain() }()
	if _, err := m.ensureDeployment(t.Context()); err != nil {
		t.Fatal(err)
	}
	audit := adminaudit.WithSource(t.Context(), adminaudit.Source{CredentialID: "reset-test", ActorLabel: "operator", RequestID: "request", TraceID: "trace"})
	if err := owner.Deployment.StartReset(audit, id, deployment.ResetRequest{ExpectedGeneration: 1, Clear: deployment.ResetForce}); err != nil {
		t.Fatal(err)
	}
	if err := m.resetStep(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(published) != 1 || published[0] != 2 {
		t.Fatal("reset did not publish its committed generation", published)
	}
	m.mu.Lock()
	current := m.config
	m.mu.Unlock()
	if current.InstallationID != id || current.Generation != 2 || current.Provider != nil {
		t.Fatal("reset did not retire the old configuration", current.InstallationID, current.Generation)
	}
	if _, err := m.deploymentService.View(t.Context()); !errors.Is(err, errCommittedRead) {
		t.Fatal("the read after the reset commit did not fail", err)
	}
}

func TestCommittedResetViewStopsOwnerWithoutLease(t *testing.T) {
	id := uuid.NewString()
	reader := &strictDeploymentReader{t: t, snapshot: func(context.Context) (deployment.Snapshot, error) {
		return deployment.Snapshot{Record: deployment.Record{InstallationID: id, WebManaged: true, Generation: 1}}, nil
	}}
	deployments, operations := deploymentOperations(t, &strictDeploymentStorage{t: t}, reader, &strictExecutionStorage{t: t})
	m, err := newRuntimeManager(Owner{Lease: lostLease{}, Deployment: operations}, deployments, reader, nil, runtimegateway.NewRegistry(), NewDeferredRuntimeProvider(id, func(context.Context) (*RuntimeProvider, error) { return nil, nil }))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { m.stop(); m.drain() }()
	view, err := m.committedView(t.Context())
	if !errors.Is(err, ErrExecutionUnavailable) || view.InstallationID != "" || view.Generation != 0 {
		t.Fatal("committed view answered without the lease", view, err)
	}
	select {
	case failure := <-m.failed:
		if !errors.Is(failure, pgunit.ErrLeaseClosed) {
			t.Fatal("owner stopped for another failure", failure)
		}
	default:
		t.Fatal("lost lease did not stop the owner")
	}
}

func TestSandboxResetChangesReturnViewReadAfterCommit(t *testing.T) {
	_, owner, deployments, reader := resetManagerStore(t)
	id := initializeE2BDeployment(t, owner)
	m, err := newRuntimeManager(owner, deployments, reader, nil, runtimegateway.NewRegistry(), NewDeferredRuntimeProvider(id, func(context.Context) (*RuntimeProvider, error) { return nil, nil }))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { m.stop(); m.drain() }()
	worker := &Worker{runtimes: m}
	audit := adminaudit.WithSource(t.Context(), adminaudit.Source{CredentialID: "reset-test", ActorLabel: "operator", RequestID: "request", TraceID: "trace"})
	started, err := worker.StartSandboxReset(audit, deployment.ResetRequest{ExpectedGeneration: 1, Clear: deployment.ResetForce})
	if err != nil {
		t.Fatal(err)
	}
	read, err := deployments.View(t.Context())
	if err != nil || started.Reset == nil || started.Reset.Clear != "force" || started.Generation != 1 || !reflect.DeepEqual(started, read) {
		t.Fatal("start returned a view other than the committed reset", started, read, err)
	}
	cancelled, err := worker.CancelSandboxReset(audit, 1)
	if err != nil {
		t.Fatal(err)
	}
	read, err = deployments.View(t.Context())
	if err != nil || cancelled.Reset != nil || cancelled.Generation != 1 || cancelled.Provider != "e2b" || !reflect.DeepEqual(cancelled, read) {
		t.Fatal("cancel returned a view other than the committed cancellation", cancelled, read, err)
	}
}
