package integration

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto/prototest"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/execution"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

func awaitTurnDeclaration(t *testing.T, h *dispatchHarness, declaration proto.SupportedAgentKind) {
	t.Helper()
	h.write("", proto.TypeHeartbeat, proto.HeartbeatPayload{HomeRemoval: proto.CapabilityUnsupported, SupportedAgentKinds: []proto.SupportedAgentKind{declaration}})
	awaitDaemonRemoteCondition(t, t.Context(), 3*time.Second, "same-connection declaration update", func() bool {
		peer, err := h.registry.LookupDevice(h.device.ID)
		if err != nil {
			return false
		}
		current, found, known := peer.AgentKindStatus(declaration.Kind)
		return found && known && current == declaration
	})
}

func assertFreshTurnRejected(t *testing.T, h *dispatchHarness, turn, reason string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if _, err := h.bound().Run(ctx, h.tenant, h.session.ID, turn); err == nil || !strings.Contains(err.Error(), reason) {
		t.Fatalf("fresh Turn did not use current declaration: %v", err)
	}
	stored, err := sessionAdapter(h.s).GetTurn(t.Context(), h.tenant, h.session.ID, turn)
	if err != nil || stored.Status != sessions.TurnQueued {
		t.Fatal("rejected fresh Turn was claimed", stored, err)
	}
}

func TestTurnRetainsMessageDeclaration(t *testing.T) {
	for _, change := range []string{"narrow", "unavailable", "widen", "reprepare"} {
		t.Run(change, func(t *testing.T) {
			h := newDispatchHarness(t)
			declaration := proto.SupportedAgentKind{Kind: "codex", Available: true, Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{
				EnvironmentNone: proto.CapabilitySupported, NativeSessionRecovery: proto.CapabilitySupported,
				MessageImages: proto.CapabilitySupported,
			})}
			widen := change == "widen" || change == "reprepare"
			if widen {
				declaration.Capabilities.MessageImages = proto.CapabilityUnsupported
			}
			awaitTurnDeclaration(t, h, declaration)
			first := h.message("first", "Start")
			running := h.run(t.Context(), first.TurnID)
			if change == "reprepare" {
				frame, original := readyExecutorAttempt(t, h)
				declaration.Capabilities.MessageImages = proto.CapabilitySupported
				awaitTurnDeclaration(t, h, declaration)
				h.write(frame.ID, proto.TypePreparationStatus, proto.PreparationStatusPayload{State: "rejected", Operation: proto.TypeExecutionStart, ErrorCode: "executor_unavailable"})
				next, replacement := readyExecutorAttempt(t, h)
				if next.ID == frame.ID || replacement.ExecutorID == original.ExecutorID || replacement.RunID != first.TurnID {
					t.Fatal("repreparation changed Turn or reused Executor", replacement)
				}
				h.write(next.ID, proto.TypePreparationStatus, proto.PreparationStatusPayload{Handle: replacement.Handle, ExecutorID: replacement.ExecutorID, Revision: 2, State: "started", RunID: replacement.RunID})
			} else {
				h.read(testExecutionRequest)
				switch change {
				case "narrow":
					declaration.Capabilities.MessageImages = proto.CapabilityUnsupported
				case "unavailable":
					declaration.Available = false
				case "widen":
					declaration.Capabilities.MessageImages = proto.CapabilitySupported
				}
				awaitTurnDeclaration(t, h, declaration)
			}
			image := imageAdmissionBatch()[1]
			receipts, err := submitInputs(t.Context(), h.s, h.tenant, h.session.ID, "image", []sessions.Input{image})
			if err != nil || receipts[0].TurnID != first.TurnID {
				t.Fatal(receipts, err)
			}
			if widen {
				turn := h.finished(running, sessions.TurnFailed)
				var outcome execution.Result
				if json.Unmarshal(turn.Outcome, &outcome) != nil || outcome.ErrorCode != "message_input_unsupported" || outcome.AppliedThrough != first.Sequence {
					t.Fatal("heartbeat granted unadmitted image steering", string(turn.Outcome))
				}
			} else {
				var steer proto.PromptSteerPayload
				if h.read(proto.TypePromptSteer).DecodePayload(&steer) != nil || !steer.Input.HasImages() {
					t.Fatal("admitted image did not reach Runtime", steer)
				}
				h.write(first.TurnID, proto.TypePromptSteerAck, proto.PromptSteerAckPayload{InputID: steer.InputID, Accepted: true})
				h.write(first.TurnID, proto.TypeDone, proto.DonePayload{Metadata: map[string]any{proto.DoneMetaAgentSessionID: "retained-message-native"}})
				turn := h.finished(running, sessions.TurnCompleted)
				var outcome execution.Result
				if json.Unmarshal(turn.Outcome, &outcome) != nil || outcome.AppliedThrough != receipts[0].Sequence {
					t.Fatal("image receipt did not advance the Turn", string(turn.Outcome))
				}
			}
			next, err := submitInputs(t.Context(), h.s, h.tenant, h.session.ID, "fresh-image", []sessions.Input{image})
			if err != nil || next[0].TurnID == first.TurnID {
				t.Fatal("new input did not create a fresh Turn", next, err)
			}
			if !widen {
				reason := "message images"
				if change == "unavailable" {
					reason = "available"
				}
				assertFreshTurnRejected(t, h, next[0].TurnID, reason)
				return
			}
			running = h.run(t.Context(), next[0].TurnID)
			var start testExecution
			if h.read(testExecutionRequest).DecodePayload(&start) != nil || !start.Input.HasImages() {
				t.Fatal("fresh Turn did not admit newly supported image", start)
			}
			h.write(next[0].TurnID, proto.TypeDone, proto.DonePayload{})
			h.finished(running, sessions.TurnCompleted)
		})
	}
}

func TestTurnRetainsFunctionDeclaration(t *testing.T) {
	for _, change := range []string{"narrow", "unavailable", "widen"} {
		for _, image := range []bool{false, true} {
			name := change + "/text"
			if image {
				name = change + "/image"
			}
			t.Run(name, func(t *testing.T) {
				h := newFunctionHarness(t)
				declaration := proto.SupportedAgentKind{Kind: "codex", Available: true, Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{
					EnvironmentNone: proto.CapabilitySupported, NativeSessionRecovery: proto.CapabilitySupported,
					FunctionTools: proto.CapabilitySupported, FunctionResultImages: proto.CapabilitySupported,
				})}
				if change == "widen" {
					declaration.Capabilities.FunctionResultImages = proto.CapabilityUnsupported
				}
				awaitTurnDeclaration(t, h, declaration)
				first := h.message("first", "Call the function")
				running := h.run(t.Context(), first.TurnID)
				h.read(testExecutionRequest)
				switch change {
				case "narrow":
					declaration.Capabilities.FunctionTools = proto.CapabilityUnsupported
					declaration.Capabilities.FunctionResultImages = proto.CapabilityUnsupported
				case "unavailable":
					declaration.Available = false
				case "widen":
					declaration.Capabilities.FunctionResultImages = proto.CapabilitySupported
				}
				awaitTurnDeclaration(t, h, declaration)
				for attempt := range 2 {
					h.write(first.TurnID, proto.TypeFunctionCall, proto.FunctionCallPayload{CallID: "lookup", Name: "lookup_ticket", Arguments: json.RawMessage(`{}`)})
					state := functionState(t, h, 1)
					content := `{"success":true,"output":"answer"}`
					if image {
						content = `{"success":true,"output":[{"type":"input_image","image_url":"data:image/png;base64,AA=="}]}`
					}
					id := state.RequiredActions[0].CallID
					if err := SubmitFixtureFunctionResult(t.Context(), h.s, h.tenant, h.session.ID, first.TurnID, id, json.RawMessage(content)); err != nil {
						t.Fatal(err)
					}
					denied := attempt == 0 && change == "widen" && image
					if denied {
						turn := h.finished(running, sessions.TurnFailed)
						var outcome execution.Result
						if json.Unmarshal(turn.Outcome, &outcome) != nil || outcome.ErrorCode != "function_result_invalid" {
							t.Fatal("heartbeat granted unadmitted function image", string(turn.Outcome))
						}
					} else {
						var result proto.FunctionResultPayload
						if h.read(proto.TypeFunctionResult).DecodePayload(&result) != nil || result.CallID != "lookup" || !result.Success || len(result.Content) != 1 {
							t.Fatal("function result did not reach Runtime", result)
						}
						if image && (result.Content[0].ImageURL == nil || *result.Content[0].ImageURL != "data:image/png;base64,AA==") || !image && (result.Content[0].Text == nil || *result.Content[0].Text != "answer") {
							t.Fatal("function content changed", result.Content)
						}
						h.write(first.TurnID, proto.TypeInteractionDecisionAck, proto.InteractionDecisionAckPayload{DeliveryID: result.DeliveryID, Applied: true})
						h.write(first.TurnID, proto.TypeDone, proto.DonePayload{Metadata: map[string]any{proto.DoneMetaAgentSessionID: "retained-functions-native"}})
						h.finished(running, sessions.TurnCompleted)
					}
					saved, err := FixtureFunctionCall(t.Context(), h.s.pool, h.tenant, h.session.ID, first.TurnID, id)
					if err != nil || saved.Applied == denied {
						t.Fatal("function receipt disagrees with admitted delivery", saved.Applied, err)
					}
					if attempt == 1 {
						break
					}
					first = h.message("fresh", "Call again")
					if change != "widen" {
						reason := "function tools"
						if change == "unavailable" {
							reason = "available"
						}
						assertFreshTurnRejected(t, h, first.TurnID, reason)
						break
					}
					running = h.run(t.Context(), first.TurnID)
					h.read(testExecutionRequest)
				}
			})
		}
	}
}

func TestPreparedTurnKeepsPreclaimDeclaration(t *testing.T) {
	h, pending := preparedDispatchHarness(t)
	result := runPreparedDispatch(h, t.Context(), pending)
	frame := h.read(proto.TypeExecutionPrepare)
	handle := acknowledgePreparation(h, frame.ID)
	caps := workerEnvironmentCapabilities()
	caps.MessageImages = proto.CapabilitySupported
	awaitFixtureCapabilities(t, h, caps)
	start := readyPreparedDispatch(t, h, frame.ID, handle)
	h.write(frame.ID, proto.TypePreparationStatus, proto.PreparationStatusPayload{Handle: handle, Revision: 3, State: "started", RunID: start.RunID})
	if _, err := submitInputs(t.Context(), h.s, h.tenant, h.session.ID, "image", imageAdmissionBatch()[1:]); err != nil {
		t.Fatal(err)
	}
	got := awaitPreparedDispatch(t, result)
	var outcome execution.Result
	if got.err != nil || got.run.Turn.Status != sessions.TurnFailed || json.Unmarshal(got.run.Turn.Outcome, &outcome) != nil || outcome.ErrorCode != "message_input_unsupported" {
		t.Fatal("final preclaim widened the preparation declaration", got.err, string(got.run.Turn.Outcome))
	}
	assertPreparationReleased(t, h, frame.ID, handle)
}
