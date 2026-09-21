package store_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/execution"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

func enableWorkerEnvironment(t *testing.T, h *dispatchHarness) {
	t.Helper()
	h.write("", proto.TypeHeartbeat, proto.HeartbeatPayload{SupportedAgentKinds: []proto.SupportedAgentKind{{Kind: "codex", Available: true, Capabilities: workerEnvironmentCapabilities()}}})
	awaitDaemonRemoteCondition(t, t.Context(), 3*time.Second, "worker preparation capability", func() bool {
		peer, err := h.registry.LookupDevice(h.device.ID)
		if err != nil {
			return false
		}
		info, _, _ := peer.AgentKindStatus("codex")
		return info.Capabilities.Preparation && info.Capabilities.EnvironmentNone
	})
}

func workerEnvironmentCapabilities() proto.AgentKindCapabilities {
	return proto.AgentKindCapabilities{Streaming: true, Steering: true, DurableTurns: true, DurableInputReceipts: true, EnvironmentNone: true, WebSearchControl: true, TextVerbosity: true, ExecutionControls: true, SubagentControl: true, ToolObservations: true, Preparation: true, LocalEnvironment: true, LocalEnvironmentNetworkPolicy: true, WorkspaceReadPreparation: true, WorkspaceOutputExport: true}
}

func workerEnvironmentReservation(t *testing.T, h *dispatchHarness) store.EnvironmentInputReservation {
	t.Helper()
	pending := unboundWorkerEnvironmentReservation(t, h)
	session, err := h.s.GetSession(t.Context(), h.tenant, pending.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	h.environments[session.ID] = connectFixtureRuntime(t, h, session)
	return pending
}

func unboundWorkerEnvironmentReservation(t *testing.T, h *dispatchHarness) store.EnvironmentInputReservation {
	t.Helper()
	session, err := h.s.CreateSession(t.Context(), h.tenant, store.CreateSessionInput{Creator: store.FixtureCreator(), Engine: "codex", IdempotencyKey: uuid.NewString(), Configuration: json.RawMessage(`{"agent":{"model":"test-model"},"environment":{"type":"self_hosted","workspace_directory":"/workspace"}}`)})
	if err != nil {
		t.Fatal(err)
	}
	pending, err := h.s.ReserveEnvironmentInput(t.Context(), h.tenant, session.ID, "work", []store.Input{{Kind: "message", Payload: json.RawMessage(`{"text":"first"}`)}})
	if err != nil {
		t.Fatal(err)
	}
	return pending
}

func workerFrames(t *testing.T, runtimes ...*dispatchHarness) <-chan proto.Envelope {
	t.Helper()
	frames := make(chan proto.Envelope, 64)
	for _, h := range runtimes {
		go func() {
			for {
				var env proto.Envelope
				if h.conn.ReadJSON(&env) != nil {
					return
				}
				select {
				case frames <- env:
				case <-t.Context().Done():
					return
				}
			}
		}()
	}
	return frames
}

func workerRuntimeForPreparation(t *testing.T, h *dispatchHarness, frame proto.Envelope) *dispatchHarness {
	t.Helper()
	var input proto.ExecutionPreparePayload
	if frame.DecodePayload(&input) != nil {
		t.Fatal("invalid worker preparation")
	}
	for _, candidate := range h.environments {
		if input.Configuration.AgentStateKey == "agents-api-"+candidate.session.ID && input.Configuration.LocalEnvironment != nil && input.Configuration.LocalEnvironment.ID == candidate.device.EnvironmentID {
			return candidate
		}
	}
	t.Fatal("worker preparation escaped its enrolled Runtime")
	return nil
}

func nextWorkerFrame(t *testing.T, frames <-chan proto.Envelope, kind string) proto.Envelope {
	t.Helper()
	select {
	case frame, ok := <-frames:
		if !ok || frame.Type != kind {
			t.Fatal("unexpected worker frame", frame.Type, kind)
		}
		return frame
	case <-time.After(15 * time.Second):
		t.Fatal("worker did not send", kind)
		return proto.Envelope{}
	}
}

func awaitWorkerEnvironmentRun(t *testing.T, ctx context.Context, s *store.Store, tenant string, pending store.EnvironmentInputReservation) execution.EnvironmentRun {
	t.Helper()
	var run execution.EnvironmentRun
	awaitDaemonRemoteCondition(t, ctx, 5*time.Minute, "worker terminal Environment Turn", func() bool {
		var err error
		run.Reservation, err = s.GetEnvironmentInputReservation(ctx, tenant, pending.SessionID, pending.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(run.Reservation.Receipts) == 0 {
			return false
		}
		run.Turn, err = s.GetTurn(ctx, tenant, pending.SessionID, run.Reservation.Receipts[0].TurnID)
		if err != nil {
			t.Fatal(err)
		}
		return run.Turn.Status == store.TurnCompleted || run.Turn.Status == store.TurnFailed || run.Turn.Status == store.TurnCancelled
	})
	return run
}
