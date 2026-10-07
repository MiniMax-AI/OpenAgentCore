package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto/prototest"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

func claudeSession(t *testing.T, h *dispatchHarness, configuration string, prebound bool) {
	t.Helper()
	var err error
	h.session, err = h.s.CreateSession(t.Context(), h.tenant, WithFixtureModelProvider(sessions.CreateSession{Creator: FixtureCreator(), Engine: "claude_sdk", IdempotencyKey: "claude", Configuration: json.RawMessage(configuration)}))
	if err != nil {
		t.Fatal(err)
	}
	if prebound {
		if err := bindSessionDevice(t, h.s, h.tenant, h.session.ID, h.device.ID); err != nil {
			t.Fatal(err)
		}
	}
}

func claudeHeartbeat(t *testing.T, h *dispatchHarness, ready bool) {
	t.Helper()
	caps := prototest.Capabilities(proto.AgentKindCapabilities{EnvironmentNone: proto.CapabilitySupported, SubagentControl: proto.CapabilitySupported, FunctionTools: proto.CapabilitySupported})
	h.write("", proto.TypeHeartbeat, proto.HeartbeatPayload{HomeRemoval: proto.CapabilityUnsupported, SupportedAgentKinds: []proto.SupportedAgentKind{{Kind: "claude_sdk", Available: ready, Capabilities: caps}}})
	deadline := time.Now().Add(3 * time.Second)
	for {
		peer, _ := h.registry.LookupDevice(h.device.ID)
		info, found, known := peer.AgentKindStatus("claude_sdk")
		if known && found && info.Available == ready {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("Claude capability heartbeat missing")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestClaudeWorkerSelectsStoredEngineAndRestrictiveCapabilities(t *testing.T) {
	for _, prebound := range []bool{false, true} {
		t.Run(fmt.Sprint(prebound), func(t *testing.T) {
			h := newFunctionHarness(t)
			claudeSession(t, h, functionConfiguration, prebound)
			input := h.message("start", "Look up ticket")
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			worker := startWorker(t, ctx, h.s, h.d)
			done := make(chan error, 1)
			go func() { done <- worker.Run(ctx) }()
			defer func() {
				cancel()
				select {
				case <-done:
				case <-time.After(10 * time.Second):
					t.Error("worker did not stop")
				}
			}()
			queued := func() {
				time.Sleep(650 * time.Millisecond)
				turn, err := sessionAdapter(h.s).GetTurn(ctx, h.tenant, h.session.ID, input.TurnID)
				if err != nil || turn.Status != sessions.TurnQueued {
					t.Fatal(turn, err)
				}
				if !prebound {
					if _, err := sessionAdapter(h.s).GetSessionDevice(ctx, h.tenant, h.session.ID); !errors.Is(err, sessions.ErrNotFound) {
						t.Fatal("bound an incapable device", err)
					}
				}
			}
			queued() // A fully capable Codex descriptor cannot execute a Claude Session.
			claudeHeartbeat(t, h, false)
			queued() // An unavailable Claude descriptor cannot execute it either.
			claudeHeartbeat(t, h, true)
			var prompt proto.PromptRequestPayload
			if h.read(testExecutionRequest).DecodePayload(&prompt) != nil {
				t.Fatal("invalid prompt")
			}
			if prompt.AgentKind != "claude_sdk" || len(prompt.FunctionTools) != 1 || !prompt.DisableExecutionEnvironment || !prompt.DisableSubagents || prompt.ExecutionControls == nil || *prompt.ExecutionControls != (proto.ExecutionControls{WebSearch: "disabled", TextVerbosity: "medium"}) {
				t.Fatal(prompt)
			}
			h.write(input.TurnID, proto.TypeDone, proto.DonePayload{Metadata: map[string]any{proto.DoneMetaAgentSessionID: "claude-native"}})
			waitTurn(t, h, input.TurnID, sessions.TurnCompleted)
			bound, err := sessionAdapter(h.s).GetSessionExecutionBinding(ctx, h.tenant, h.session.ID)
			if err != nil || bound.NativeSessionID != "claude-native" {
				t.Fatal(bound, err)
			}
		})
	}
}

func TestClaudeDispatcherRejectsUnsupportedConfigurationBeforeClaim(t *testing.T) {
	for _, configuration := range []string{
		strings.Replace(functionConfiguration, `"model":`, `"text":{"verbosity":"high"},"model":`, 1),
		strings.Replace(functionConfiguration, `"type":"object",`, ``, 1),
	} {
		t.Run(configuration, func(t *testing.T) {
			h := newDispatchHarness(t)
			claudeSession(t, h, configuration, true)
			claudeHeartbeat(t, h, true)
			input := h.message("start", "Run")
			if result := <-h.run(t.Context(), input.TurnID); result.err == nil {
				t.Fatal("unsupported configuration claimed")
			}
			turn, err := sessionAdapter(h.s).GetTurn(t.Context(), h.tenant, h.session.ID, input.TurnID)
			if err != nil || turn.Status != sessions.TurnQueued {
				t.Fatal(turn, err)
			}
		})
	}
}

func TestClaudeInvalidImageResultRejectsWholeBatchBeforePersistence(t *testing.T) {
	h := newDispatchHarness(t)
	claudeSession(t, h, functionConfiguration, false)
	worker := startOwnedWorker(t, t.Context(), h.s, h.d, h.owner())
	defer func() { ctx, cancel := context.WithCancel(context.Background()); cancel(); _ = worker.Run(ctx) }()
	input := h.message("start", "Run")
	if _, err := transitionTurn(t.Context(), h.s, h.tenant, h.session.ID, input.TurnID, sessions.TurnTransition{ExpectedStatus: sessions.TurnQueued, Status: sessions.TurnInProgress}); err != nil {
		t.Fatal(err)
	}
	call := sessions.FunctionCall{CallID: "public-call", ExecutorCallID: "native-call", Name: "lookup_ticket", Arguments: json.RawMessage(`{"ticket":"42"}`)}
	if err := h.owner().Sessions.RecordFunctionCall(t.Context(), h.tenant, h.session.ID, input.TurnID, call); err != nil {
		t.Fatal(err)
	}
	result := func(raw string) sessions.Input {
		payload, err := json.Marshal(sessions.FunctionResultInput{TurnID: input.TurnID, CallID: call.CallID, Result: json.RawMessage(raw)})
		if err != nil {
			t.Fatal(err)
		}
		return sessions.Input{Kind: "tool_result", Payload: payload}
	}
	batch := []sessions.Input{messageInput("Follow up"), result(`{"success":true,"output":[{"type":"input_image","image_url":"data:image/png;base64,AA=="}]}`), {Kind: "cancel", Payload: json.RawMessage(`{}`)}}
	if _, err := worker.SubmitInputs(t.Context(), h.tenant, h.session.ID, "batch", batch); !errors.Is(err, sessions.ErrInvalidInput) {
		t.Fatal(err)
	}
	saved, err := FixtureFunctionCall(t.Context(), h.s.pool, h.tenant, h.session.ID, input.TurnID, call.CallID)
	if err != nil || saved.Result != nil || saved.Applied {
		t.Fatal(saved, err)
	}
	turn, err := sessionAdapter(h.s).GetTurn(t.Context(), h.tenant, h.session.ID, input.TurnID)
	if err != nil || turn.Status != sessions.TurnWaiting || !turn.CancelRequestedAt.IsZero() {
		t.Fatal(turn, err)
	}
	history, err := sessionAdapter(h.s).ListTurnInputs(t.Context(), h.tenant, h.session.ID, input.TurnID, 0, 100)
	if err != nil || len(history) != 1 {
		t.Fatal(history, err)
	}
	batch[1] = result(`{"success":false,"output":[{"type":"input_text","text":"Partial result"}],"error":"Tool failed"}`)
	receipts, err := worker.SubmitInputs(t.Context(), h.tenant, h.session.ID, "batch", batch)
	if err != nil || len(receipts) != 3 {
		t.Fatal(receipts, err)
	}
	retry, err := worker.SubmitInputs(t.Context(), h.tenant, h.session.ID, "batch", batch)
	if err != nil || len(retry) != 3 || !retry[0].Replayed {
		t.Fatal(retry, err)
	}
}
