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
	var preparations atomic.Int32
	configuration := execution.NewDeferredRuntimeProvider(id, func(ctx context.Context) (*execution.RuntimeProvider, error) {
		setup, err := s.GetSandboxSetup(ctx)
		if err != nil || setup.Provider == "" {
			return nil, err
		}
		return &execution.RuntimeProvider{InstallationID: id, ProviderKind: setup.Provider, Generation: setup.Generation, Mode: setup.Mode, AdmissionPaused: setup.AdmissionPaused, CoreURL: "https://core.example/api/v1", BackendFingerprint: setup.BackendFingerprint, Provider: p}, nil
	}, func(ctx context.Context, setup store.SandboxSetup) (execution.PreparedRuntimeDeployment, error) {
		preparations.Add(1)
		if fail.Load() {
			return execution.PreparedRuntimeDeployment{}, errors.New("fixture provider unavailable")
		}
		return execution.PreparedRuntimeDeployment{Config: &execution.RuntimeProvider{InstallationID: setup.InstallationID, ProviderKind: setup.Provider, Mode: setup.Mode, AdmissionPaused: setup.AdmissionPaused, CoreURL: "https://core.example/api/v1", BackendFingerprint: setup.BackendFingerprint, Provider: p}}, nil
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
	if _, err := w.InitializeSandboxDeployment(t.Context(), store.SandboxDeploymentSetupRequest{DeploymentSpec: store.SandboxDeploymentTestSpec("docker"), Provider: "docker"}); err == nil {
		t.Fatal("rejected initial provider configuration was committed")
	}
	empty, err := s.GetRuntimeDeployment(t.Context())
	if err != nil || empty.Provider != "" || empty.Generation != 0 || empty.Specification != nil || empty.Resources != (store.SandboxDeploymentResources{}) {
		t.Fatal("failed initial candidate changed the deployment", err)
	}
	fail.Store(false)
	if _, err := w.InitializeSandboxDeployment(t.Context(), store.SandboxDeploymentSetupRequest{DeploymentSpec: store.SandboxDeploymentTestSpec("docker"), Provider: "docker"}); err != nil {
		t.Fatal(err)
	}
	prepared := preparations.Load()
	if _, err := w.InitializeSandboxDeployment(t.Context(), store.SandboxDeploymentSetupRequest{DeploymentSpec: store.SandboxDeploymentTestSpec("docker"), Provider: "docker"}); err == nil || preparations.Load() != prepared {
		t.Fatal("stale identical POST reached provider preparation", err)
	}
	enrollment, err := store.EnrollmentTestToken(s.CreateRuntimeEnrollment(t.Context(), store.RuntimeNodeCapacity{MaxActive: 2, MaxRetained: 4}))
	if err != nil {
		t.Fatal(err)
	}
	node := store.RuntimeNodeEnrollment{NodeID: uuid.NewString(), Name: "retained candidate fixture", Provider: "docker", Credential: strings.Repeat("n", 64), BackendFingerprint: strings.Repeat("b", 64), DeploymentGeneration: 1, SpecificationDigest: store.SandboxDeploymentTestSpec("docker").Digest("docker")}
	if _, err := s.EnrollRuntimeNode(t.Context(), enrollment, node); err != nil {
		t.Fatal(err)
	}
	previous, err := s.GetRuntimeDeployment(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	changed := store.SandboxDeploymentSetupRequest{DeploymentSpec: store.SandboxDeploymentTestSpec("docker"), Provider: "docker"}
	changed.Resources.CPUs++
	input := store.SandboxDeploymentUpdateRequest{ExpectedGeneration: 1, SandboxDeploymentSetupRequest: changed}
	fail.Store(true)
	if _, err := w.UpdateSandboxDeployment(store.SandboxResetTestContext(t.Context()), input); err == nil {
		t.Fatal("failed activation reported success")
	}
	view, err := s.GetRuntimeDeployment(t.Context())
	if err != nil || !reflect.DeepEqual(view, previous) {
		t.Fatal("failed candidate changed committed configuration", view, err)
	}
	if _, err := s.AuthenticateRuntimeNode(t.Context(), node.NodeID, node.Credential); err != nil {
		t.Fatal("rejected candidate retired the previous node", err)
	}
	fail.Store(false)
	// Inject a real final SQL failure after successful
	// candidate preparation; the surviving owner keeps its active deployment.
	if _, err := pool.Exec(t.Context(), `CREATE FUNCTION reject_reset_fixture_update() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.generation > OLD.generation THEN RAISE EXCEPTION 'fixture commit rejected'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_reset_fixture_update BEFORE UPDATE ON runtime_deployment FOR EACH ROW EXECUTE FUNCTION reject_reset_fixture_update()`); err != nil {
		t.Fatal(err)
	}
	if _, err := w.UpdateSandboxDeployment(store.SandboxResetTestContext(t.Context()), input); err == nil {
		t.Fatal("failed final transaction accepted")
	}
	if _, err := pool.Exec(t.Context(), `DROP TRIGGER reject_reset_fixture_update ON runtime_deployment; DROP FUNCTION reject_reset_fixture_update()`); err != nil {
		t.Fatal(err)
	}
	if err := w.ReconcileManagedRuntimes(t.Context()); err != nil {
		t.Fatal("failed commit left manager barrier closed", err)
	}
	if view, err := s.GetRuntimeDeployment(t.Context()); err != nil || view.Generation != 1 || view.Provider != "docker" {
		t.Fatal("failed commit replaced provider", view, err)
	}
	if _, err := w.UpdateSandboxDeployment(store.SandboxResetTestContext(t.Context()), input); err != nil {
		t.Fatal(err)
	}
	reset := func(generation uint64) store.RuntimeDeploymentView {
		t.Helper()
		if _, err := w.StartSandboxReset(store.SandboxResetTestContext(t.Context()), store.SandboxResetRequest{ExpectedGeneration: generation, Clear: "force"}); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(12 * time.Second)
		for time.Now().Before(deadline) {
			view, err := s.GetRuntimeDeployment(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if view.Provider == "" && view.Reset == nil && view.Generation == generation+1 {
				return view
			}
			select {
			case err := <-done:
				t.Fatalf("reset stopped owner: %v", err)
			case <-time.After(20 * time.Millisecond):
			}
		}
		t.Fatal("reset completion blocked (including possible manager self-drain)")
		return store.RuntimeDeploymentView{}
	}
	empty = reset(2)
	cloud := store.SandboxDeploymentSetupRequest{ExpectedGeneration: empty.Generation, DeploymentSpec: store.SandboxDeploymentTestSpec("e2b"), Provider: "e2b", E2B: &sandbox.E2BConfiguration{APIKey: "fixture-api-key", Template: "runtime:" + uuid.NewString()}}
	if _, err := w.InitializeSandboxDeployment(t.Context(), cloud); err != nil {
		t.Fatal("setup after unconfigured publication", err)
	}
	tenant, session, environment := managedSession(t, s)
	allocation, err := w.ProvisionEnvironment(t.Context(), tenant, environment.ID, id)
	if err != nil || allocation.NodeID != "" || allocation.State != "running" {
		t.Fatal("direct provider not available after resume", allocation, err)
	}
	next := store.SandboxDeploymentUpdateRequest{ExpectedGeneration: 4, SandboxDeploymentSetupRequest: store.SandboxDeploymentSetupRequest{DeploymentSpec: store.SandboxDeploymentTestSpec("docker"), Provider: "docker"}}
	if _, err := w.UpdateSandboxDeployment(store.SandboxResetTestContext(t.Context()), next); !errors.Is(err, store.ErrSandboxDeploymentConflict) {
		t.Fatal("dirty switch accepted", err)
	}
	if err := s.DeleteSession(t.Context(), tenant, session.ID); err != nil {
		t.Fatal(err)
	}
	reconcileManagedState(t, w, s, tenant, environment.ID, "released")
	empty = reset(4)
	next.SandboxDeploymentSetupRequest.ExpectedGeneration = empty.Generation
	if _, err := w.InitializeSandboxDeployment(t.Context(), next.SandboxDeploymentSetupRequest); err != nil {
		t.Fatal("clean setup after reset", err)
	}
	select {
	case err := <-done:
		t.Fatalf("provider switch stopped Worker: %v", err)
	default:
	}
}
