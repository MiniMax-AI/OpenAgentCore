package integration

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/execution"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimegateway"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/google/uuid"
)

func TestManagedRuntimeAutomaticBootstrapRecoversCommittedSessions(t *testing.T) {
	s, _ := newManagedTestStore(t)
	key := webDeployment(t, s, "e2b")
	tenant, idle, idleEnvironment := managedSession(t, s)
	initial, err := s.CreateSession(t.Context(), tenant, sessions.CreateSession{Creator: FixtureCreator(), Engine: "codex", IdempotencyKey: uuid.NewString(), Configuration: json.RawMessage(`{"agent":{"model":"test"},"environment":{"type":"openai_hosted"}}`), InitialInputs: []sessions.Input{messageInput("hello")}})
	if err != nil {
		t.Fatal(err)
	}
	_, deleted, deletedEnvironment := managedSession(t, s)
	if err := sessionService(t, s).DeleteSession(t.Context(), sessions.DeleteSessionCommand{TenantID: deleted.TenantID, SessionID: deleted.ID}); err != nil {
		t.Fatal(err)
	}
	p := &lifecycleProvider{resources: map[string]sandbox.Info{}}
	start := func() *execution.Worker { return startWebWorker(t, s, runtimegateway.NewRegistry(), nil, key, p, nil) }
	stop := func(w *execution.Worker) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_ = w.Run(ctx)
	}
	w := start()
	closed := false
	t.Cleanup(func() {
		if !closed {
			stop(w)
		}
	})
	// Both Sessions committed before this Worker existed, including one without inputs.
	for range 100 {
		if err := w.ReconcileManagedRuntimes(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	idleOwner, err := deploymentStore(s).EnvironmentAllocation(t.Context(), deployment.AllocationKey{TenantID: tenant, EnvironmentID: idleEnvironment.ID})
	if err != nil || idleOwner.State != "running" {
		t.Fatal("idle creation was stranded", idleOwner, err)
	}
	initialOwner, err := deploymentStore(s).EnvironmentAllocation(t.Context(), deployment.AllocationKey{TenantID: tenant, EnvironmentID: initial.Environment.ID})
	if err != nil || initialOwner.State != "running" {
		t.Fatal("initial creation was stranded", initialOwner, err)
	}
	if _, err := deploymentStore(s).EnvironmentAllocation(t.Context(), deployment.AllocationKey{TenantID: deleted.TenantID, EnvironmentID: deletedEnvironment.ID}); err == nil {
		t.Fatal("deleted Session provisioned")
	}
	waiting, err := sessionAdapter(s).GetSession(t.Context(), tenant, initial.ID)
	if err != nil || waiting.LastTurn != nil || waiting.EnvironmentInputActivity != nil {
		t.Fatal("compute existence claimed input readiness", waiting, err)
	}
	quiet, err := sessionAdapter(s).GetSession(t.Context(), tenant, idle.ID)
	if err != nil || quiet.LastTurn != nil || quiet.EnvironmentInputActivity != nil {
		t.Fatal("idle creation fabricated work", err)
	}
	creates := p.creates
	stop(w)
	closed = true
	next := start()
	defer stop(next)
	for range 100 {
		if err := next.ReconcileManagedRuntimes(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if p.creates != creates {
		t.Fatal("restart repeated bootstrap", creates, p.creates)
	}
	for _, owner := range []deployment.Allocation{idleOwner, initialOwner} {
		got, err := deploymentStore(s).EnvironmentAllocation(t.Context(), deployment.AllocationKey{TenantID: tenant, EnvironmentID: owner.EnvironmentID})
		if err != nil || got.ID != owner.ID || got.DeviceID != owner.DeviceID {
			t.Fatal("restart replaced allocation identity", got, err)
		}
	}
}
