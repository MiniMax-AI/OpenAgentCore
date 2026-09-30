package execution

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/projectpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/projects"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/providercontract"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/microsandbox"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/adminaudit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/identity"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtime"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimegateway"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

// Provider callbacks inspect the real database at the instant destructive
// cleanup is invoked. They do not create containers or claim native evidence.
type waitingCleanupProvider struct {
	sandbox.SandboxProvider
	beforeKill func()
}

func (p waitingCleanupProvider) GetInfo(_ context.Context, r sandbox.Reference) (sandbox.Info, error) {
	return sandbox.Info{Reference: r, ProviderID: r.AllocationID, State: "running", BootstrapComplete: true, CreateSettled: true}, nil
}
func (p waitingCleanupProvider) Kill(context.Context, sandbox.Reference) error {
	p.beforeKill()
	return nil
}

func (waitingCleanupCheckpoint) ProviderOperations() providercontract.Operations {
	return microsandbox.Operations()
}

type waitingCleanupCheckpoint struct {
	sandbox.CheckpointProvider
	beforeKill func()
}

func (p waitingCleanupCheckpoint) KillCompute(context.Context, sandbox.Reference, sandbox.Compute) error {
	p.beforeKill()
	return nil
}

func TestArchiveWaitingCleanupReceiptBarrier(t *testing.T) {
	for _, scenario := range []struct {
		name                 string
		checkpoint, delivery bool
	}{{"Kill_no_delivery", false, false}, {"KillCompute_no_delivery", true, false}, {"Kill_live_delivery", false, true}, {"KillCompute_live_delivery", true, true}} {
		t.Run(scenario.name, func(t *testing.T) {
			checkpoint := scenario.checkpoint
			s, leased, _, _, pool := resetManagerStoreDB(t, nil)
			writer := leased.Store
			installation := initializeE2BDeployment(t, leased)
			projectID := uuid.NewString()
			audit := adminaudit.WithSource(t.Context(), adminaudit.Source{CredentialID: "fixture-admin", ProjectID: projectID, RequestID: uuid.NewString(), TraceID: uuid.NewString()})
			management, err := projects.NewService(projectpg.New(pgunit.NewPool(pool)))
			if err != nil {
				t.Fatal(err)
			}
			project, err := management.CreateProject(audit, projects.CreateProject{ID: projectID, Name: "Cleanup diagnosis"})
			if err != nil {
				t.Fatal(err)
			}
			session, err := s.CreateSession(t.Context(), project.TenantID, sessions.CreateSession{Creator: identity.Subject{Kind: "service_account", ID: "fixture"}, Engine: "codex", IdempotencyKey: uuid.NewString(), Configuration: json.RawMessage(`{"agent":{"model":"test-model"},"environment":{"type":"openai_hosted","network":{"access":"disabled"}}}`), ModelProvider: &v1.ModelProviderInput{Protocol: "responses", BaseURL: "https://model.fixture.example/v1", APIKey: "fixture-key"}, ModelProviderSource: v1.ModelProviderSourceSession})
			if err != nil {
				t.Fatal(err)
			}
			secret := uuid.NewString()
			owner, err := writer.ReserveRuntimeAllocation(t.Context(), project.TenantID, session.Environment.ID, installation, runtimedevice.HashCredential(secret))
			if err != nil {
				t.Fatal(err)
			}
			owner, err = writer.ObserveRuntimeRunning(t.Context(), owner)
			if err != nil {
				t.Fatal(err)
			}
			input, err := s.SubmitMessage(t.Context(), project.TenantID, session.ID, "start", json.RawMessage(`{"text":"run"}`))
			if err != nil {
				t.Fatal(err)
			}
			// This fixture isolates lifecycle ordering. Protocol-driven waiting is
			// independently exercised in TestArchiveWaitingCancellationReceipts.
			for _, transition := range []sessions.TurnTransition{{ExpectedStatus: sessions.TurnQueued, Status: sessions.TurnInProgress}, {ExpectedStatus: sessions.TurnInProgress, Status: sessions.TurnWaiting}} {
				if _, err := writer.TransitionTurn(t.Context(), project.TenantID, session.ID, input.TurnID, transition); err != nil {
					t.Fatal(err)
				}
			}
			currentCompute := sandbox.Compute{ID: uuid.NewString(), Name: owner.ID + "-g0"}
			if checkpoint {
				state, _ := json.Marshal(runtimeCompute{Current: currentCompute})
				owner, err = writer.SetRuntimeCompute(t.Context(), owner, "running", state, nil, 0)
				if err != nil {
					t.Fatal(err)
				}
			}
			registry := runtimegateway.NewRegistry()
			if scenario.delivery {
				server := httptest.NewUnstartedServer(nil)
				wsURL := "ws://" + server.Listener.Addr().String() + "/api/v1/agent-daemon/ws"
				credentials, heartbeat := testSessions(t, pool, testCredentialCipher(t))
				handler, liveRegistry, err := runtime.NewGateway(credentials, heartbeat, s, wsURL)
				if err != nil {
					t.Fatal(err)
				}
				registry = liveRegistry
				server.Config.Handler = handler
				server.Start()
				t.Cleanup(func() { runtime.CloseConnections(registry); server.Close() })
				u, _ := url.Parse(wsURL)
				u.RawQuery = url.Values{"device_id": {owner.DeviceID}, "version": {proto.Version}}.Encode()
				conn, _, err := websocket.DefaultDialer.Dial(u.String(), http.Header{"Authorization": {"Bearer " + secret}})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { conn.Close() })
				var peer *runtimegateway.Session
				for end := time.Now().Add(3 * time.Second); ; {
					peer, err = registry.LookupDevice(owner.DeviceID)
					if err == nil {
						break
					}
					if time.Now().After(end) {
						t.Fatal(err)
					}
					time.Sleep(time.Millisecond)
				}
				release, err := peer.TrackExecutionDelivery(input.TurnID)
				if err != nil {
					t.Fatal(err)
				}
				defer release()
			}
			if _, err := writer.ArchiveManagedSession(audit, project.TenantID, session.ID, 1); err != nil {
				t.Fatal(err)
			}
			owner, err = s.GetRuntimeAllocation(t.Context(), project.TenantID, session.Environment.ID)
			if err != nil {
				t.Fatal(err)
			}
			kills := 0
			expectedStatus := sessions.TurnWaiting
			provider := waitingCleanupProvider{beforeKill: func() {
				kills++
				turn, err := s.GetTurn(t.Context(), project.TenantID, session.ID, input.TurnID)
				if err != nil || turn.Status != expectedStatus || turn.CancelRequestedAt.IsZero() || (turn.CompletedAt.IsZero() != (expectedStatus == sessions.TurnWaiting)) {
					t.Fatal("cleanup observed unexpected terminal state", turn, err)
				}
				allocation, err := s.GetRuntimeAllocation(t.Context(), project.TenantID, session.Environment.ID)
				if err != nil || allocation.State != "cleanup_pending" {
					t.Fatal("Kill bypassed durable cleanup ownership", allocation, err)
				}
			}}
			reader, _ := testSessions(t, pool, testCredentialCipher(t))
			lifecycle := &runtimeLifecycle{store: writer, sessions: reader, sessionExecution: leased.Sessions, lease: leased.Lease, registry: registry, config: RuntimeProvider{InstallationID: installation, Provider: provider}, connections: map[string]*runtimeConnection{}}
			if checkpoint {
				lifecycle.config.Provider = waitingCleanupCheckpoint{beforeKill: provider.beforeKill}
			}
			if err := lifecycle.observe(t.Context(), owner); err != nil {
				t.Fatal(err)
			}
			if scenario.delivery {
				if kills != 0 {
					t.Fatal("destroyed compute before cancellation committed")
				}
				pending, err := s.GetRuntimeAllocation(t.Context(), project.TenantID, session.Environment.ID)
				if err != nil || pending.State != "cleanup_pending" {
					t.Fatal(pending, err)
				}
				// Controlled terminal receipt fixture; no native cancellation claim.
				if _, err := writer.TransitionTurn(t.Context(), project.TenantID, session.ID, input.TurnID, sessions.TurnTransition{ExpectedStatus: sessions.TurnWaiting, Status: sessions.TurnCancelled}); err != nil {
					t.Fatal(err)
				}
				expectedStatus = sessions.TurnCancelled
				if err := lifecycle.observe(t.Context(), owner); err != nil {
					t.Fatal(err)
				}
			}
			after, err := s.GetRuntimeAllocation(t.Context(), project.TenantID, session.Environment.ID)
			if err != nil || after.State != "released" || kills != 1 {
				t.Fatal("cleanup did not release", after, kills, err)
			}
			turn, err := s.GetTurn(t.Context(), project.TenantID, session.ID, input.TurnID)
			if err != nil || turn.Status != expectedStatus || (turn.CompletedAt.IsZero() != (expectedStatus == sessions.TurnWaiting)) {
				t.Fatal("cleanup should not fabricate cancellation", turn, err)
			}
		})
	}
}
