package store_test

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
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
		setup, err := s.GetSandboxSetup(ctx)
		if err != nil || setup.Provider == "" {
			return nil, err
		}
		return &execution.RuntimeProvider{InstallationID: id, ProviderKind: setup.Provider, Generation: setup.Generation, Mode: setup.Mode, Maintenance: setup.Maintenance, CoreURL: setup.CoreURL + "/api/v1", BackendFingerprint: setup.BackendFingerprint, Provider: p}, nil
	}, func(ctx context.Context, setup store.SandboxSetup) (execution.PreparedRuntimeDeployment, error) {
		if fail.Load() {
			return execution.PreparedRuntimeDeployment{}, errors.New("fixture provider unavailable")
		}
		return execution.PreparedRuntimeDeployment{Config: &execution.RuntimeProvider{InstallationID: setup.InstallationID, ProviderKind: setup.Provider, Mode: setup.Mode, Maintenance: setup.Maintenance, CoreURL: setup.CoreURL + "/api/v1", BackendFingerprint: setup.BackendFingerprint, Provider: p}}, nil
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
	fail.Store(true)
	if _, err := w.InitializeSandboxDeployment(t.Context(), store.SandboxDeploymentSetupRequest{DeploymentSpec: store.SandboxDeploymentTestSpec("docker"), Provider: "docker", CoreURL: "https://core.example"}); err == nil {
		t.Fatal("rejected initial provider configuration was committed")
	}
	empty, err := s.GetRuntimeDeployment(t.Context())
	if err != nil || empty.Provider != "" || empty.Generation != 0 || empty.Specification != nil || empty.Resources != (store.SandboxDeploymentResources{}) {
		t.Fatal("failed initial candidate changed the deployment", err)
	}
	fail.Store(false)
	if _, err := w.InitializeSandboxDeployment(t.Context(), store.SandboxDeploymentSetupRequest{DeploymentSpec: store.SandboxDeploymentTestSpec("docker"), Provider: "docker", CoreURL: "https://core.example"}); err != nil {
		t.Fatal(err)
	}
	enrollment, _, err := s.CreateRuntimeEnrollment(t.Context(), store.RuntimeNodeCapacity{MaxActive: 2, MaxRetained: 4})
	if err != nil {
		t.Fatal(err)
	}
	node := store.RuntimeNodeEnrollment{NodeID: uuid.NewString(), Name: "retained candidate fixture", Provider: "docker", Credential: strings.Repeat("n", 64), BackendFingerprint: strings.Repeat("b", 64), DeploymentGeneration: 1, SpecificationDigest: store.SandboxDeploymentTestSpec("docker").Digest("docker")}
	if _, err := s.EnrollRuntimeNode(t.Context(), enrollment, node); err != nil {
		t.Fatal(err)
	}
	if _, err := w.SetSandboxMaintenance(t.Context(), store.SandboxMaintenanceRequest{Maintenance: true, ExpectedGeneration: 1}); err != nil {
		t.Fatal(err)
	}
	previous, err := s.GetRuntimeDeployment(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	input := store.SandboxDeploymentUpdateRequest{ExpectedGeneration: 1, SandboxDeploymentSetupRequest: store.SandboxDeploymentSetupRequest{DeploymentSpec: store.SandboxDeploymentTestSpec("e2b"), Provider: "e2b", CoreURL: "https://core.example", E2B: &store.SandboxE2BConfiguration{APIKey: "fixture-api-key", Template: "runtime:" + uuid.NewString()}}}
	fail.Store(true)
	if _, err := w.UpdateSandboxDeployment(t.Context(), input); err == nil {
		t.Fatal("failed activation reported success")
	}
	view, err := s.GetRuntimeDeployment(t.Context())
	if err != nil || !reflect.DeepEqual(view, previous) {
		t.Fatal("failed candidate changed committed configuration", view, err)
	}
	if _, err := s.AuthenticateRuntimeNode(t.Context(), node.NodeID, node.Credential); err != nil {
		t.Fatal("rejected candidate retired the previous node", err)
	}
	if _, err := w.SetSandboxMaintenance(t.Context(), store.SandboxMaintenanceRequest{ExpectedGeneration: 2}); err == nil {
		t.Fatal("failed activation resumed")
	}
	fail.Store(false)
	if _, err := w.UpdateSandboxDeployment(t.Context(), input); err != nil {
		t.Fatal(err)
	}
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
	next := store.SandboxDeploymentUpdateRequest{ExpectedGeneration: 2, SandboxDeploymentSetupRequest: store.SandboxDeploymentSetupRequest{DeploymentSpec: store.SandboxDeploymentTestSpec("docker"), Provider: "docker", CoreURL: "https://core.example"}}
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
