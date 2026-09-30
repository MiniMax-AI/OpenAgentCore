package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/gateway"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/adminaudit"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/execution"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/runtime"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

// Controlled protocol fixtures, not native/model acceptance. Allocation setup
// uses the real execution lease; the real dispatcher consumes a function request
// through the real authenticated WebSocket and persists waiting before archive.
func TestArchiveWaitingCancellationReceipts(t *testing.T) {
	for _, scenario := range []string{"receipt_without_heartbeat", "heartbeat_before_receipt", "done_heartbeat_ack", "ack_commit_blocked", "rotated", "expired", "transport_lost", "negative_ack", "missing_outcome", "revoke_before_archive", "cancel_revoke_archive", "revoke_after_archive", "revoke_concurrent_archive"} {
		t.Run(scenario, func(t *testing.T) {
			heartbeat := scenario != "receipt_without_heartbeat"
			s, pool := store.NewManagedTestStore(t)
			lease, err := s.AcquireExecutionLease(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := lease.Close(context.Background()); err != nil {
					t.Error(err)
				}
			})
			writer := lease.Store()
			installation := uuid.NewString()
			if err := writer.ClaimWebSandboxDeployment(t.Context(), installation); err != nil {
				t.Fatal(err)
			}
			if _, err := writer.InitializeSandboxDeployment(t.Context(), installation, store.SandboxDeploymentSetupRequest{DeploymentSpec: store.SandboxDeploymentTestSpec("e2b"), Provider: "e2b", E2B: &sandbox.E2BConfiguration{APIKey: "fixture", Template: "runtime:" + uuid.NewString()}}); err != nil {
				t.Fatal(err)
			}
			projectID := uuid.NewString()
			auditCtx := adminaudit.WithSource(t.Context(), adminaudit.Source{CredentialID: "fixture-admin", ProjectID: projectID, RequestID: uuid.NewString(), TraceID: uuid.NewString()})
			project, err := s.CreateProject(auditCtx, projectID, "Archive diagnosis")
			if err != nil {
				t.Fatal(err)
			}
			configuration := strings.Replace(functionConfiguration, `"type":"none"`, `"type":"openai_hosted","network":{"access":"disabled"}`, 1)
			session, err := s.CreateSession(t.Context(), project.TenantID, store.WithFixtureModelProvider(store.CreateSessionInput{Creator: store.FixtureCreator(), Engine: "codex", IdempotencyKey: uuid.NewString(), Configuration: json.RawMessage(configuration)}))
			if err != nil {
				t.Fatal(err)
			}
			secret := uuid.NewString()
			owner, err := writer.ReserveRuntimeAllocation(t.Context(), project.TenantID, session.Environment.ID, installation, device.HashCredential(secret))
			if err != nil {
				t.Fatal(err)
			}
			for _, step := range []func(context.Context, store.RuntimeAllocation) (store.RuntimeAllocation, error){writer.ObserveRuntimeRunning, writer.SettleRuntimeCreation} {
				owner, err = step(t.Context(), owner)
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := writer.BindSessionDevice(t.Context(), project.TenantID, session.ID, owner.DeviceID); err != nil {
				t.Fatal(err)
			}
			generation := uuid.NewString()
			if err := writer.ReplaceEnvironmentConnection(t.Context(), project.TenantID, session.Environment.ID, generation); err != nil {
				t.Fatal(err)
			}
			if err := writer.ObserveEnvironmentConnection(t.Context(), project.TenantID, session.Environment.ID, generation, 1, true); err != nil {
				t.Fatal(err)
			}
			server := httptest.NewUnstartedServer(nil)
			wsURL := "ws://" + server.Listener.Addr().String() + "/api/v1/agent-daemon/ws"
			handler, registry, err := runtime.NewGateway(s, wsURL)
			if err != nil {
				t.Fatal(err)
			}
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
			h := &dispatchHarness{t: t, s: s, tenant: project.TenantID, session: session, conn: conn, registry: registry, d: &execution.Dispatcher{Store: writer, Registry: registry}}
			capabilities := workerEnvironmentCapabilities()
			capabilities.FunctionTools = true
			h.write("", proto.TypeHeartbeat, proto.HeartbeatPayload{SupportedAgentKinds: []proto.SupportedAgentKind{{Kind: "codex", Available: true, Capabilities: capabilities}}})
			var peer *gateway.Session
			for deadline := time.Now().Add(3 * time.Second); ; {
				peer, err = registry.LookupDevice(owner.DeviceID)
				if err == nil {
					info, _, known := peer.AgentKindStatus("codex")
					if known && info.Capabilities.FunctionTools {
						break
					}
				}
				if time.Now().After(deadline) {
					t.Fatal("initial heartbeat missing")
				}
				time.Sleep(time.Millisecond)
			}
			pending, err := s.ReserveEnvironmentInput(t.Context(), h.tenant, session.ID, "pending", []store.Input{{Kind: "message", Payload: json.RawMessage(`{"text":"first"}`)}, {Kind: "message", Payload: json.RawMessage(`{"text":"second"}`)}})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			result := runPreparedDispatch(h, ctx, pending)
			frame := h.read(proto.TypeExecutionPrepare)
			handle := acknowledgePreparation(h, frame.ID)
			start := readyPreparedDispatch(t, h, frame.ID, handle)
			h.write(frame.ID, proto.TypePreparationStatus, proto.PreparationStatusPayload{Handle: handle, Revision: 3, State: "started", RunID: start.RunID})
			input := store.InputReceipt{TurnID: start.RunID}
			h.write(input.TurnID, proto.TypeFunctionCall, proto.FunctionCallPayload{CallID: "pending", Name: "lookup_ticket", Arguments: json.RawMessage(`{"ticket":"42"}`)})
			state := functionState(t, h, 1)
			if state.LastTurn.Status != store.TurnWaiting {
				t.Fatal(state.LastTurn)
			}

			if scenario == "cancel_revoke_archive" {
				if _, err := s.RequestCancel(t.Context(), h.tenant, session.ID, "ordinary-cancel"); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "revoke_before_archive" || scenario == "cancel_revoke_archive" {
				if err := s.RevokeDevice(t.Context(), h.tenant, owner.DeviceID); err != nil {
					t.Fatal(err)
				}
			}
			var revokeDone chan error
			if scenario == "revoke_concurrent_archive" {
				revokeDone = make(chan error, 1)
				go func() { revokeDone <- s.RevokeDevice(t.Context(), h.tenant, owner.DeviceID) }()
			}

			archived, err := writer.ArchiveManagedSession(auditCtx, h.tenant, session.ID, 1)
			if err != nil || archived.State != "cleanup_pending" {
				t.Fatal(archived, err)
			}
			if revokeDone != nil {
				if err := <-revokeDone; err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "revoke_after_archive" {
				if err := s.RevokeDevice(t.Context(), h.tenant, owner.DeviceID); err != nil {
					t.Fatal(err)
				}
			}
			// A repeat archive and cleanup must not recreate an explicitly
			// cleared marker, nor erase the marker from a fresh archive.
			repeatAudit := adminaudit.WithSource(t.Context(), adminaudit.Source{CredentialID: "fixture-admin", ProjectID: projectID, RequestID: uuid.NewString(), TraceID: uuid.NewString()})
			if _, err := writer.ArchiveManagedSession(repeatAudit, h.tenant, session.ID, 1); err != nil {
				t.Fatal(err)
			}
			if _, err := writer.RequestRuntimeCleanup(t.Context(), owner); err != nil {
				t.Fatal(err)
			}

			current, err := s.GetTurn(t.Context(), h.tenant, session.ID, input.TurnID)
			if err != nil || current.Status != store.TurnWaiting || current.CancelRequestedAt.IsZero() {
				t.Fatal("archive must request rather than invent cancellation", current, err)
			}
			if _, err := gateway.NewAuthenticator(s).AuthenticateBearer(t.Context(), owner.DeviceID, secret); !errors.Is(err, gateway.ErrAuthUnknownDevice) {
				t.Fatal("archive allowed renewed authority", err)
			}
			rejected, response, dialErr := websocket.DefaultDialer.Dial(u.String(), http.Header{"Authorization": {"Bearer " + secret}})
			if rejected != nil {
				rejected.Close()
			}
			if response != nil {
				response.Body.Close()
			}
			if dialErr == nil || response == nil || response.StatusCode != http.StatusUnauthorized {
				t.Fatal("revoked Runtime reconnected")
			}
			drain, err := s.ArchivedCancellationReceipt(t.Context(), owner.DeviceID, secret, nil)
			if err != nil || drain.RunID != "" {
				t.Fatal("unowned delivery got receipt permission", drain, err)
			}
			drain, err = s.ArchivedCancellationReceipt(t.Context(), owner.DeviceID, device.HashCredential(secret), []string{input.TurnID})
			if err != nil || (drain.RunID == input.TurnID) == strings.Contains(scenario, "revoke") {
				t.Fatal("archive revocation causality lost", drain, err)
			}
			if drain.RunID != "" && !drain.Deadline.Equal(current.CancelRequestedAt.Add(device.ArchivedCancellationReceiptLimit)) {
				t.Fatal("archive renewed cancellation deadline")
			}
			// Explicitly observe cancel delivery before inducing transport loss. This
			// proves even a sent cancellation can lose its receipt; no ticker timing guess.
			var request proto.PromptCancelPayload
			if err := h.read(proto.TypePromptCancel).DecodePayload(&request); err != nil {
				t.Fatal(err)
			}
			if request.DeliveryID == "" {
				t.Fatal("missing cancel delivery identity")
			}
			if scenario == "rotated" {
				if _, err := pool.Exec(t.Context(), "UPDATE devices SET credential_hash=$2 WHERE id=$1", owner.DeviceID, device.HashCredential(uuid.NewString())); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "expired" {
				if _, err := pool.Exec(t.Context(), "UPDATE turns SET cancel_requested_at=clock_timestamp()-interval '21 seconds' WHERE id=$1", input.TurnID); err != nil {
					t.Fatal(err)
				}
			}
			var unlockCommit func()
			if scenario == "ack_commit_blocked" {
				tx, err := pool.Begin(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				if _, err := tx.Exec(t.Context(), "SELECT session_id FROM session_devices WHERE session_id=$1 FOR UPDATE", session.ID); err != nil {
					t.Fatal(err)
				}
				unlockCommit = func() {
					if err := tx.Rollback(context.Background()); err != nil {
						t.Fatal(err)
					}
				}
				defer func() { _ = tx.Rollback(context.Background()) }()
			}

			if scenario == "done_heartbeat_ack" {
				h.write(input.TurnID, proto.TypeDone, proto.DonePayload{})
			}
			closedCase := scenario == "rotated" || scenario == "expired" || scenario == "transport_lost" || strings.Contains(scenario, "revoke")
			if scenario == "transport_lost" {
				h.conn.Close()
			} else if heartbeat && scenario != "ack_commit_blocked" {
				h.write("", proto.TypeHeartbeat, proto.HeartbeatPayload{})
			}
			if !closedCase {
				ack := proto.InteractionDecisionAckPayload{DeliveryID: request.DeliveryID, Applied: true, Outcome: &proto.DonePayload{Usage: proto.Usage{InputTokens: 17}, Metadata: map[string]any{proto.DoneMetaAgentSessionID: "cancelled-native"}}}
				if scenario == "negative_ack" {
					ack.Applied = false
					ack.ErrorCode = "cancel_failed"
					ack.Outcome = nil
				}
				if scenario == "missing_outcome" {
					ack.Outcome = nil
				}
				h.write(input.TurnID, proto.TypeInteractionDecisionAck, ack)
			}
			if unlockCommit != nil {
				// Observe actual SQL lock contention, not an assumed timing delay.
				for deadline := time.Now().Add(3 * time.Second); ; {
					var blocked bool
					if err := pool.QueryRow(t.Context(), "SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query ILIKE '%session_devices%')").Scan(&blocked); err != nil {
						t.Fatal(err)
					}
					if blocked {
						break
					}
					if time.Now().After(deadline) {
						t.Fatal("terminal commit never blocked")
					}
					time.Sleep(time.Millisecond)
				}
				h.write("", proto.TypeHeartbeat, proto.HeartbeatPayload{})
				draining, err := peer.DrainArchivedCancellation(t.Context())
				if err != nil || !draining || peer.IsClosed() {
					t.Fatal("ACK lost drain before terminal commit", draining, err)
				}
				unlockCommit()
			}

			got := awaitPreparedDispatch(t, result)
			wantStatus := store.TurnCancelled
			if closedCase || scenario == "negative_ack" || scenario == "missing_outcome" {
				wantStatus = store.TurnFailed
			}
			if got.err != nil || got.run.Turn.Status != wantStatus {
				t.Fatal(got.run.Turn.Status, got.err, string(got.run.Turn.Outcome))
			}
			var outcome execution.Result
			if err := json.Unmarshal(got.run.Turn.Outcome, &outcome); err != nil || (wantStatus == store.TurnCancelled && outcome.Done.Usage.InputTokens != 17) {
				t.Fatal("receipt lost usage", string(got.run.Turn.Outcome), err)
			}

			var receipts int
			if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM turn_events WHERE turn_id=$1 AND kind='cancel_receipt'", input.TurnID).Scan(&receipts); err != nil {
				t.Fatal(err)
			}
			wantReceipts := 1
			if closedCase {
				wantReceipts = 0
			}
			if receipts != wantReceipts {
				t.Fatal("durable cancellation receipt mismatch", receipts, wantReceipts)
			}
			// The original cleanup owner survives every delivery outcome; only
			// provider receipts can release its resources.
			allocation, err := s.GetRuntimeAllocation(t.Context(), h.tenant, session.Environment.ID)
			if err != nil || allocation.State != "cleanup_pending" {
				t.Fatal(allocation, err)
			}
			var revoked bool
			if err := pool.QueryRow(t.Context(), "SELECT revoked_at IS NOT NULL FROM devices WHERE id=$1", owner.DeviceID).Scan(&revoked); err != nil || !revoked {
				t.Fatal(revoked, err)
			}
		})
	}
}
