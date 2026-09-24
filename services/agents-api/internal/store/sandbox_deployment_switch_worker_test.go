package store_test

import (
	"bytes"
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/gateway"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/credentialcrypto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/execution"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

func TestSandboxWorkerSwitchesAndRecoversFailedActivation(t *testing.T) {
	_, pool := store.NewManagedTestStore(t)
	cipher, _ := credentialcrypto.New(bytes.Repeat([]byte{7}, 32))
	s := store.NewWithCredentialCipher(pool, cipher)
	id := uuid.NewString()
	p := &lifecycleProvider{resources: map[string]sandbox.Info{}}
	var fail atomic.Bool
	configuration := execution.NewDeferredRuntimeProvider(id, func(ctx context.Context) (*execution.RuntimeProvider, error) {
		if fail.Load() {
			return nil, errors.New("fixture provider unavailable")
		}
		setup, err := s.GetSandboxSetup(ctx)
		if err != nil || setup.Provider == "" {
			return nil, err
		}
		return &execution.RuntimeProvider{InstallationID: id, ProviderKind: setup.Provider, Generation: setup.Generation, Mode: setup.Mode, Maintenance: setup.Maintenance, CoreURL: setup.CoreURL + "/api/v1", BackendFingerprint: setup.BackendFingerprint, Provider: p}, nil
	})
	w, err := execution.StartWorker(t.Context(), &execution.Dispatcher{Store: s, Registry: gateway.NewRegistry(), ManagedRuntimes: configuration})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("worker shutdown blocked")
		}
	})
	if _, err := w.InitializeSandboxDeployment(t.Context(), store.SandboxDeploymentSetupRequest{Provider: "docker", CoreURL: "https://core.example"}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.SetSandboxMaintenance(t.Context(), store.SandboxMaintenanceRequest{Maintenance: true, ExpectedGeneration: 1}); err != nil {
		t.Fatal(err)
	}
	input := store.SandboxDeploymentUpdateRequest{ExpectedGeneration: 1, SandboxDeploymentSetupRequest: store.SandboxDeploymentSetupRequest{Provider: "e2b", CoreURL: "https://core.example", E2B: &store.SandboxE2BConfiguration{APIKey: "fixture-api-key", Template: "runtime:" + uuid.NewString()}}}
	fail.Store(true)
	if _, err := w.UpdateSandboxDeployment(t.Context(), input); err == nil {
		t.Fatal("failed activation reported success")
	}
	view, err := s.GetRuntimeDeployment(t.Context())
	if err != nil || view.Generation != 2 || !view.Maintenance || view.Provider != "e2b" {
		t.Fatal("failed activation lost persisted maintenance", view, err)
	}
	if _, err := w.SetSandboxMaintenance(t.Context(), store.SandboxMaintenanceRequest{ExpectedGeneration: 2}); err == nil {
		t.Fatal("failed activation resumed")
	}
	fail.Store(false)
	if _, err := w.SetSandboxMaintenance(t.Context(), store.SandboxMaintenanceRequest{ExpectedGeneration: 2}); err != nil {
		t.Fatal(err)
	}
	tenant, session, environment := managedSession(t, s)
	allocation, err := w.ProvisionEnvironment(t.Context(), tenant, environment.ID, id)
	if err != nil || allocation.NodeID != "" || allocation.State != "running" {
		t.Fatal("direct provider not available after resume", allocation, err)
	}
	if _, err := w.SetSandboxMaintenance(t.Context(), store.SandboxMaintenanceRequest{Maintenance: true, ExpectedGeneration: 2}); err != nil {
		t.Fatal(err)
	}
	next := store.SandboxDeploymentUpdateRequest{ExpectedGeneration: 2, SandboxDeploymentSetupRequest: store.SandboxDeploymentSetupRequest{Provider: "docker", CoreURL: "https://core.example"}}
	if _, err := w.UpdateSandboxDeployment(t.Context(), next); !errors.Is(err, store.ErrSandboxDeploymentConflict) {
		t.Fatal("dirty switch accepted", err)
	}
	if err := s.DeleteSession(t.Context(), tenant, session.ID); err != nil {
		t.Fatal(err)
	}
	reconcileManagedState(t, w, s, tenant, environment.ID, "released")
	if _, err := w.UpdateSandboxDeployment(t.Context(), next); err != nil {
		t.Fatal("clean switch failed", err)
	}
	if _, err := w.SetSandboxMaintenance(t.Context(), store.SandboxMaintenanceRequest{ExpectedGeneration: 3}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		t.Fatalf("provider switch stopped Worker: %v", err)
	default:
	}
}
