package execution

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/e2b"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/adminaudit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgtest"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimegateway"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/node"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The execution lease is database-scoped, so these manager tests own a database.
// They receive the pooled Store and the execution writer built on it.
func resetManagerStore(t *testing.T) (*store.Store, *store.Store) {
	t.Helper()
	return resetManagerStoreConfig(t, nil)
}

func resetManagerStoreConfig(t *testing.T, configure func(*pgxpool.Config)) (*store.Store, *store.Store) {
	t.Helper()
	pool := pgtest.OpenIsolated(t, configure)
	cipher, err := credentialcrypto.New(bytes.Repeat([]byte{8}, 32))
	if err != nil {
		t.Fatal(err)
	}
	s := store.NewWithCredentialCipher(pool, cipher)
	writer, err := store.NewExecution(t.Context(), s)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = writer.CloseExecution(context.Background()) })
	return s, writer
}

func TestSandboxResetPageTimeoutRecoversCommittedOwner(t *testing.T) {
	s, w := resetManagerStore(t)
	id := uuid.NewString()
	if err := w.ClaimWebSandboxDeployment(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	selection := store.SandboxDeploymentSetupRequest{Provider: "e2b", Configuration: &e2b.DeploymentConfiguration{APIKey: "fixture-key", Template: "runtime:" + uuid.NewString()}}
	selection.Resources.CPUs = 2
	selection.Resources.MemoryMiB = 2048
	if _, err := w.InitializeSandboxDeployment(t.Context(), id, selection); err != nil {
		t.Fatal(err)
	}
	hub := node.NewHub(node.HubOptions{})
	defer hub.Close()
	loads := 0
	config := NewDeferredRuntimeProvider(id, func(ctx context.Context) (*RuntimeProvider, error) {
		if ctx.Err() != nil {
			t.Error("recovery inherited cancelled page")
		}
		loads++
		setup, err := s.GetSandboxSetup(ctx)
		if err != nil || setup.Provider == "" {
			return nil, err
		}
		return &RuntimeProvider{InstallationID: id, ProviderKind: setup.Provider, Mode: setup.Mode, Generation: setup.Generation, CoreURL: "https://core.example/api/v1", BackendFingerprint: setup.BackendFingerprint, Provider: hub.Proxy(uuid.NewString(), "docker", 1)}, nil
	})
	m, err := newRuntimeManager(w, runtimegateway.NewRegistry(), config)
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
	reset, err := w.StartSandboxReset(audit, id, store.SandboxResetRequest{ExpectedGeneration: 1, Clear: "force"})
	if err != nil {
		t.Fatal(err)
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
	current, err := s.GetRuntimeDeployment(t.Context())
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
	current, err = s.GetRuntimeDeployment(t.Context())
	if err != nil || current.Reset != nil || current.Provider != "" || current.Generation != 2 {
		t.Fatal("next tick did not finish", current, err)
	}
}
