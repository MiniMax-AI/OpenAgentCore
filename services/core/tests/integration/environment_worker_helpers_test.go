package integration

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto/prototest"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/execution"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/google/uuid"
)

func enableWorkerEnvironment(t *testing.T, h *dispatchHarness) {
	t.Helper()
	h.write("", proto.TypeHeartbeat, proto.HeartbeatPayload{HomeRemoval: proto.CapabilityUnsupported, SupportedAgentKinds: []proto.SupportedAgentKind{{Kind: "codex", Available: true, Capabilities: workerEnvironmentCapabilities()}}})
	awaitDaemonRemoteCondition(t, t.Context(), 3*time.Second, "worker capabilities", func() bool {
		peer, err := h.registry.LookupDevice(h.device.ID)
		if err != nil {
			return false
		}
		info, _, _ := peer.AgentKindStatus("codex")
		return info.Capabilities == workerEnvironmentCapabilities()
	})
}

func workerEnvironmentCapabilities() proto.AgentKindCapabilities {
	return prototest.Capabilities(proto.AgentKindCapabilities{EnvironmentNone: proto.CapabilitySupported, TextVerbosity: proto.CapabilitySupported, LocalEnvironment: proto.CapabilitySupported})
}

func workerEnvironmentReservation(t *testing.T, h *dispatchHarness) sessions.EnvironmentInputReservation {
	t.Helper()
	pending := unboundWorkerEnvironmentReservation(t, h)
	session, err := sessionAdapter(h.s).GetSession(t.Context(), h.tenant, pending.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	h.environments[session.ID] = connectFixtureRuntime(t, h, session)
	return pending
}

func unboundWorkerEnvironmentReservation(t *testing.T, h *dispatchHarness) sessions.EnvironmentInputReservation {
	t.Helper()
	session, err := h.s.CreateSession(t.Context(), h.tenant, WithFixtureModelProvider(sessions.CreateSession{Creator: FixtureCreator(), Engine: "codex", IdempotencyKey: uuid.NewString(), Configuration: json.RawMessage(`{"agent":{"model":"test-model"},"environment":{"type":"self_hosted","workspace_directory":"/workspace"}}`)}))
	if err != nil {
		t.Fatal(err)
	}
	pending, err := sessionService(t, h.s).ReserveEnvironmentInput(t.Context(), h.tenant, session.ID, "work", []sessions.Input{messageInput("first")})
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
				if h.assignmentFrame(env) {
					continue
				}
				var keep bool
				env, keep = h.executionFrame(env)
				if !keep {
					continue
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

func awaitWorkerEnvironmentRun(t *testing.T, ctx context.Context, s *Store, tenant string, pending sessions.EnvironmentInputReservation) execution.EnvironmentRun {
	t.Helper()
	var run execution.EnvironmentRun
	awaitDaemonRemoteCondition(t, ctx, 5*time.Minute, "worker terminal Environment Turn", func() bool {
		var err error
		run.Reservation, err = sessionAdapter(s).GetEnvironmentInputReservation(ctx, tenant, pending.SessionID, pending.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(run.Reservation.Receipts) == 0 {
			return false
		}
		run.Turn, err = sessionAdapter(s).GetTurn(ctx, tenant, pending.SessionID, run.Reservation.Receipts[0].TurnID)
		if err != nil {
			t.Fatal(err)
		}
		return run.Turn.Status == sessions.TurnCompleted || run.Turn.Status == sessions.TurnFailed || run.Turn.Status == sessions.TurnCancelled
	})
	return run
}
