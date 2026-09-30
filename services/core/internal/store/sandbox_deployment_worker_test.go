package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/execution"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimegateway"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
	"github.com/google/uuid"
)

func TestSandboxDeploymentWorkerActivatesWithoutRestart(t *testing.T) {
	s, db := newManagedTestStoreDB(t)
	deployments := fixtureDeployment(t, db)
	id := uuid.NewString()
	p := &lifecycleProvider{resources: map[string]sandbox.Info{}}
	configuration := execution.NewDeferredRuntimeProvider(id, func(ctx context.Context) (*execution.RuntimeProvider, error) {
		setup, err := deployments.Setup(ctx)
		if err != nil || setup.Provider == "" {
			return nil, err
		}
		return &execution.RuntimeProvider{InstallationID: setup.InstallationID, ProviderKind: setup.Provider, BackendFingerprint: setup.BackendFingerprint, CoreURL: "https://core.example/api/v1", Provider: p}, nil
	}, func(ctx context.Context, setup deployment.Setup) (execution.PreparedRuntimeDeployment, error) {

		return execution.PreparedRuntimeDeployment{Config: &execution.RuntimeProvider{InstallationID: setup.InstallationID, ProviderKind: setup.Provider, Mode: setup.Mode, AdmissionPaused: setup.AdmissionPaused, CoreURL: "https://core.example/api/v1", BackendFingerprint: setup.BackendFingerprint, Provider: p}}, nil
	})
	start := func() (*execution.Worker, func()) {
		t.Helper()
		w := startWorker(t, t.Context(), db, &execution.Dispatcher{Store: s, Registry: runtimegateway.NewRegistry(), ManagedRuntimes: configuration})
		var once sync.Once
		stop := func() {
			once.Do(func() { ctx, cancel := context.WithCancel(context.Background()); cancel(); _ = w.Run(ctx) })
		}
		t.Cleanup(stop)
		return w, stop
	}
	w, stop := start()
	input := sessions.CreateSession{Creator: store.FixtureCreator(), Engine: "codex", IdempotencyKey: uuid.NewString(), Configuration: json.RawMessage(`{"agent":{"model":"test"},"environment":{"type":"openai_hosted","network":{"access":"disabled"}}}`)}
	if _, err := w.CreateSession(t.Context(), uuid.NewString(), input); !errors.Is(err, execution.ErrExecutionUnavailable) {
		t.Fatal("uninitialized worker admitted hosted Session", err)
	}
	if _, err := w.InitializeSandboxDeployment(t.Context(), sandbox.Selection{DeploymentSpec: store.SandboxDeploymentTestSpec("docker"), Provider: "docker"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateSession(t.Context(), uuid.NewString(), input); !errors.Is(err, deployment.ErrNodeUnavailable) {
		t.Fatal("zero-node deployment admitted Session", err)
	}
	token, err := store.EnrollmentTestToken(deployments.CreateEnrollment(t.Context(), deployment.Capacity{MaxActive: 4, MaxRetained: 16}))
	if err != nil {
		t.Fatal(err)
	}
	nodeID := uuid.NewString()
	if _, err := deployments.Enroll(t.Context(), token, deployment.Enrollment{DeploymentGeneration: 1, SpecificationDigest: store.SandboxDeploymentTestSpec("docker").Digest("docker"), NodeID: nodeID, Name: "Remote", Provider: "docker", Credential: strings.Repeat("x", 64), BackendFingerprint: strings.Repeat("b", 64)}); err != nil {
		t.Fatal(err)
	}
	connect := func() {
		t.Helper()
		epoch := fixtureOwnerEpoch(t, db)
		connection := uuid.NewString()
		if err := deployments.ConnectNode(t.Context(), nodeID, connection, epoch); err != nil {
			t.Fatal(err)
		}
		if err := deployments.Heartbeat(t.Context(), nodeID, connection, epoch, deployment.NodeHealth{ProviderReady: true}); err != nil {
			t.Fatal(err)
		}
	}
	connect()
	tenant, _, environment := managedSession(t, s)
	allocation, err := w.ProvisionEnvironment(t.Context(), tenant, environment.ID, id)
	if err != nil || allocation.NodeID != nodeID || p.creates != 1 {
		t.Fatal("activation failed", allocation, err)
	}
	stop()
	w, _ = start()
	connect()
	replayed, err := w.ProvisionEnvironment(t.Context(), tenant, environment.ID, id)
	if err != nil || replayed.ID != allocation.ID || !replayed.Replayed || p.creates != 1 {
		t.Fatal("restart changed allocation ownership", replayed, err)
	}
}
