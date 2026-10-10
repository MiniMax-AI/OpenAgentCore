package execution

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/identity"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgtest"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimegateway"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/google/uuid"
)

type freshHintProvider struct {
	sandbox.SandboxProvider
	creates atomic.Int32
	created chan struct{}
}

type hintSessionReader struct {
	sessions.Reader
	environment sessions.Environment
}

func (r hintSessionReader) GetSessionEnvironment(ctx context.Context, _, _ string) (sessions.Environment, error) {
	return r.environment, ctx.Err()
}

func TestFreshHintRoutesAndPreservesLifecycleGuards(t *testing.T) {
	for _, scenario := range []string{"direct", "node", "closed", "switching", "cancelled", "self_hosted", "released_placement", "lookup_failed", "suspended"} {
		t.Run(scenario, func(t *testing.T) {
			m := testRuntimeManager(t)
			m.setupGate = make(chan struct{}, 1)
			m.config = RuntimeProvider{InstallationID: uuid.NewString(), ProviderKind: "e2b", Mode: "direct", Provider: &freshHintProvider{}}
			nodeID := ""
			if scenario == "node" || scenario == "released_placement" {
				m.config.ProviderKind, m.config.Mode = "docker", "nodes"
				nodeID = uuid.NewString()
			}
			node, err := m.node(nodeID)
			if err != nil {
				t.Fatal(err)
			}
			environment := sessions.Environment{ID: uuid.NewString(), Configuration: json.RawMessage(`{"type":"openai_hosted"}`)}
			if scenario == "self_hosted" {
				environment.Configuration = json.RawMessage(`{"type":"self_hosted"}`)
			}
			if scenario == "suspended" {
				environment.Initialization = "complete"
				m.config.Suspension = &RuntimeSuspensionPolicy{}
			}
			reader := &strictDeploymentReader{t: t,
				environmentAllocation: func(context.Context, deployment.AllocationKey) (deployment.Allocation, error) {
					if scenario == "lookup_failed" {
						return deployment.Allocation{}, errors.New("database unavailable")
					}
					if scenario == "suspended" {
						return deployment.Allocation{ProviderKey: m.config.InstallationID, State: "running", CreateSettled: true, ComputePhase: "suspended"}, nil
					}
					return deployment.Allocation{}, deployment.ErrNotFound
				},
				lifecyclePlacement: func(context.Context, deployment.AllocationKey) (deployment.LifecyclePlacement, error) {
					return deployment.LifecyclePlacement{Provider: m.config.ProviderKind, Mode: m.config.Mode, PlacementNodeID: nodeID, PlacementReleased: scenario == "released_placement"}, nil
				},
			}
			m.deploymentService, _ = deploymentOperations(t, &strictDeploymentStorage{t: t}, reader, &strictExecutionStorage{t: t})
			worker := &Worker{runtimes: m, dispatcher: &Dispatcher{SessionsReader: hintSessionReader{environment: environment}, DeploymentReader: reader}}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			switch scenario {
			case "closed":
				m.closed = true
			case "switching":
				m.switching = true
			case "cancelled":
				cancel()
			}
			worker.hintRuntimeWake(ctx, sessions.Session{TenantID: uuid.NewString(), ID: uuid.NewString()})
			want := 0
			if scenario == "direct" || scenario == "node" || scenario == "suspended" {
				want = 1
			}
			if got := len(node.lifecycle.wakeHints); got != want {
				t.Fatalf("hints = %d, want %d", got, want)
			}
		})
	}
}

func (p *freshHintProvider) Create(_ context.Context, b sandbox.Bootstrap) (sandbox.Info, error) {
	p.creates.Add(1)
	select {
	case p.created <- struct{}{}:
	default:
	}
	return sandbox.Info{Reference: b.Reference, ProviderID: b.AllocationID, State: "running", BootstrapComplete: true, CreateSettled: true}, nil
}

func TestFreshEnvironmentHintProvisionsWithoutMaintenanceTick(t *testing.T) {
	for _, mode := range []string{"create", "create_initial", "recovered_input", "recovered_initial"} {
		t.Run(mode, func(t *testing.T) {
			owner, deployments, reader, pool := resetManagerDB(t, nil)
			installation := initializeE2BDeployment(t, owner)
			sessionReader, sessionService := testSessions(t, pool, pgtest.CredentialKey(t))
			provider := &freshHintProvider{created: make(chan struct{}, 1)}
			m := testRuntimeManager(t)
			m.setupGate = make(chan struct{}, 1)
			m.sessions, m.sessionExecution = sessionReader, owner.Sessions
			m.deployment, m.deploymentService, m.deploymentReader = owner.Deployment, deployments, reader
			m.lease, m.registry = owner.Lease, runtimegateway.NewRegistry()
			m.config = RuntimeProvider{InstallationID: installation, Generation: 1, ProviderKind: "e2b", Mode: "direct", CoreURL: fixturePublicURL + "/api/v1", Provider: provider}
			worker := &Worker{lease: owner.Lease, runtimes: m, dispatcher: &Dispatcher{Sessions: sessionService, SessionsReader: sessionReader, DeploymentReader: reader, notifications: &executionNotifications{}}, scheduleWake: make(chan struct{}, 1)}
			node, err := m.node("")
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			done, scans := make(chan error, 1), make(chan struct{}, 4)
			// No value is ever sent to ticks: provisioning must come from a hint.
			ticks := make(chan time.Time)
			go func() {
				done <- runRuntimeMaintenance(ctx, ticks, node.lifecycle.wakeHints, func(ctx context.Context) error {
					err := node.lifecycle.reconcile(ctx)
					scans <- struct{}{}
					return err
				})
			}()
			t.Cleanup(func() { cancel(); <-done })
			<-scans // Startup scan finishes before any Session is committed.
			tenant := uuid.NewString()
			input := sessions.CreateSession{Creator: identity.Subject{Kind: "service_account", ID: "fixture"}, Engine: "codex", IdempotencyKey: uuid.NewString(), Configuration: json.RawMessage(`{"agent":{"model":"test-model"},"environment":{"type":"openai_hosted"}}`), ModelProvider: &v1.ModelProviderInput{Protocol: "responses", BaseURL: "https://model.fixture.example/v1", APIKey: "fixture-key"}, ModelProviderSource: v1.ExecutionSourceSession}
			messages := []sessions.Input{{Kind: "message", Payload: json.RawMessage(`{"input":[{"role":"user","content":[{"type":"input_text","text":"fixture"}]}]}`)}}
			if mode == "recovered_initial" || mode == "create_initial" {
				input.InitialInputs = messages
			}
			var creation sessions.Creation
			switch mode {
			case "create", "create_initial":
				// Ordinary and streaming HTTP creation share this Worker entrypoint.
				creation, err = worker.CreateSession(ctx, tenant, input)
			case "recovered_input", "recovered_initial":
				creation, err = sessionService.CreateSession(ctx, tenant, input)
			}
			session := creation.Session
			if err != nil {
				t.Fatal(err)
			}
			if mode == "recovered_input" || mode == "recovered_initial" {
				key := uuid.NewString()
				if mode == "recovered_initial" {
					if err := pool.QueryRow(ctx, "SELECT idempotency_key FROM environment_input_reservations WHERE session_id=$1 AND is_initial", session.ID).Scan(&key); err != nil {
						t.Fatal(err)
					}
				}
				inputDone := make(chan error, 20)
				inputCtx, stopInput := context.WithCancel(ctx)
				for range 20 {
					go func() {
						_, inputErr := worker.submitEnvironmentInputs(inputCtx, session, key, messages)
						inputDone <- inputErr
					}()
				}
				t.Cleanup(func() {
					stopInput()
					for range 20 {
						<-inputDone
					}
				})
			}
			select {
			case <-provider.created:
			case <-time.After(2 * time.Second):
				t.Fatal("committed Environment waited for a maintenance tick")
			}
			var requests sync.WaitGroup
			for range 20 {
				requests.Go(func() { worker.hintRuntimeWake(ctx, session) })
			}
			requests.Wait()
			<-scans // Wait for the hinted scan to settle its durable allocation.
			if got := provider.creates.Load(); got != 1 {
				t.Fatalf("concurrent hints issued %d Creates", got)
			}
		})
	}
}
