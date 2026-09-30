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

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/e2b"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/execution"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimegateway"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
	"github.com/google/uuid"
)

func TestSandboxWorkerSwitchesAndRecoversFailedActivation(t *testing.T) {
	_, pool := store.NewManagedTestStore(t)
	cipher, _ := credentialcrypto.New(bytes.Repeat([]byte{7}, 32))
	s, db := store.NewWithCredentialCipher(pool, cipher), fixtureDB{pool: pool, cipher: cipher}
	deployments := fixtureDeployment(t, db)
	id := uuid.NewString()
	p := &lifecycleProvider{resources: map[string]sandbox.Info{}}
	var fail atomic.Bool
	var preparations atomic.Int32
	configuration := execution.NewDeferredRuntimeProvider(id, func(ctx context.Context) (*execution.RuntimeProvider, error) {
		setup, err := deployments.Setup(ctx)
		if err != nil || setup.Provider == "" {
			return nil, err
		}
		return &execution.RuntimeProvider{InstallationID: id, ProviderKind: setup.Provider, Generation: setup.Generation, Mode: setup.Mode, AdmissionPaused: setup.AdmissionPaused, CoreURL: "https://core.example/api/v1", BackendFingerprint: setup.BackendFingerprint, Provider: p}, nil
	}, func(ctx context.Context, setup deployment.Setup) (execution.PreparedRuntimeDeployment, error) {
		preparations.Add(1)
		if fail.Load() {
			return execution.PreparedRuntimeDeployment{}, errors.New("fixture provider unavailable")
		}
		return execution.PreparedRuntimeDeployment{Config: &execution.RuntimeProvider{InstallationID: setup.InstallationID, ProviderKind: setup.Provider, Mode: setup.Mode, AdmissionPaused: setup.AdmissionPaused, CoreURL: "https://core.example/api/v1", BackendFingerprint: setup.BackendFingerprint, Provider: p}}, nil
	})
	w := startWorker(t, t.Context(), db, &execution.Dispatcher{Store: s, Registry: runtimegateway.NewRegistry(), ManagedRuntimes: configuration})
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
	if _, err := w.InitializeSandboxDeployment(t.Context(), sandbox.Selection{DeploymentSpec: store.SandboxDeploymentTestSpec("docker"), Provider: "docker"}); err == nil {
		t.Fatal("rejected initial provider configuration was committed")
	}
	empty, err := deployments.View(t.Context())
	if err != nil || empty.Provider != "" || empty.Generation != 0 || empty.Specification != nil || empty.Resources != (deployment.Resources{}) {
		t.Fatal("failed initial candidate changed the deployment", err)
	}
	fail.Store(false)
	if _, err := w.InitializeSandboxDeployment(t.Context(), sandbox.Selection{DeploymentSpec: store.SandboxDeploymentTestSpec("docker"), Provider: "docker"}); err != nil {
		t.Fatal(err)
	}
	prepared := preparations.Load()
	if _, err := w.InitializeSandboxDeployment(t.Context(), sandbox.Selection{DeploymentSpec: store.SandboxDeploymentTestSpec("docker"), Provider: "docker"}); err == nil || preparations.Load() != prepared {
		t.Fatal("stale identical POST reached provider preparation", err)
	}
	enrollment, err := store.EnrollmentTestToken(deployments.CreateEnrollment(t.Context(), deployment.Capacity{MaxActive: 2, MaxRetained: 4}))
	if err != nil {
		t.Fatal(err)
	}
	node := deployment.Enrollment{NodeID: uuid.NewString(), Name: "retained candidate fixture", Provider: "docker", Credential: strings.Repeat("n", 64), BackendFingerprint: strings.Repeat("b", 64), DeploymentGeneration: 1, SpecificationDigest: store.SandboxDeploymentTestSpec("docker").Digest("docker")}
	if _, err := deployments.Enroll(t.Context(), enrollment, node); err != nil {
		t.Fatal(err)
	}
	previous, err := deployments.View(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	input := sandbox.Selection{ExpectedGeneration: 1, DeploymentSpec: store.SandboxDeploymentTestSpec("docker"), Provider: "docker"}
	input.Resources.CPUs++
	fail.Store(true)
	if _, err := w.UpdateSandboxDeployment(store.SandboxResetTestContext(t.Context()), input); err == nil {
		t.Fatal("failed activation reported success")
	}
	view, err := deployments.View(t.Context())
	if err != nil || !reflect.DeepEqual(view, previous) {
		t.Fatal("failed candidate changed committed configuration", view, err)
	}
	if _, err := deployments.AuthenticateNode(t.Context(), node.NodeID, node.Credential); err != nil {
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
	if view, err := deployments.View(t.Context()); err != nil || view.Generation != 1 || view.Provider != "docker" {
		t.Fatal("failed commit replaced provider", view, err)
	}
	if _, err := w.UpdateSandboxDeployment(store.SandboxResetTestContext(t.Context()), input); err != nil {
		t.Fatal(err)
	}
	reset := func(generation uint64) deployment.View {
		t.Helper()
		if _, err := w.StartSandboxReset(store.SandboxResetTestContext(t.Context()), store.SandboxResetRequest{ExpectedGeneration: generation, Clear: "force"}); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(12 * time.Second)
		for time.Now().Before(deadline) {
			view, err := deployments.View(t.Context())
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
		return deployment.View{}
	}
	empty = reset(2)
	cloud := sandbox.Selection{ExpectedGeneration: empty.Generation, DeploymentSpec: store.SandboxDeploymentTestSpec("e2b"), Provider: "e2b", Configuration: &e2b.DeploymentConfiguration{APIKey: "fixture-api-key", Template: "runtime:" + uuid.NewString()}}
	if _, err := w.InitializeSandboxDeployment(t.Context(), cloud); err != nil {
		t.Fatal("setup after unconfigured publication", err)
	}
	tenant, session, environment := managedSession(t, s)
	allocation, err := w.ProvisionEnvironment(t.Context(), tenant, environment.ID, id)
	if err != nil || allocation.NodeID != "" || allocation.State != "running" {
		t.Fatal("direct provider not available after resume", allocation, err)
	}
	next := sandbox.Selection{ExpectedGeneration: 4, DeploymentSpec: store.SandboxDeploymentTestSpec("docker"), Provider: "docker"}
	if _, err := w.UpdateSandboxDeployment(store.SandboxResetTestContext(t.Context()), next); !errors.Is(err, deployment.ErrConflict) {
		t.Fatal("dirty switch accepted", err)
	}
	if err := s.DeleteSession(t.Context(), tenant, session.ID); err != nil {
		t.Fatal(err)
	}
	reconcileManagedState(t, w, s, tenant, environment.ID, "released")
	empty = reset(4)
	next.ExpectedGeneration = empty.Generation
	if _, err := w.InitializeSandboxDeployment(t.Context(), next); err != nil {
		t.Fatal("clean setup after reset", err)
	}
	select {
	case err := <-done:
		t.Fatalf("provider switch stopped Worker: %v", err)
	default:
	}
}
