package store_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/engine"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/execution"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

func TestStructuredOutputDispatchRechecksOperationQualification(t *testing.T) {
	h := newDispatchHarness(t)
	configuration := json.RawMessage(`{"agent":{"model":"fixture","text":{"format":{"type":"json_schema","schema":{"type":"object"}}}},"environment":{"type":"none"}}`)
	profile := engine.Profile{Placements: []string{"none"}, StructuredOutput: true}
	policy := execution.Policy{Engines: engine.NewCatalog(map[string]engine.Profile{"fixture_harness": profile})}
	if err := policy.ValidateSessionConfiguration("fixture_harness", configuration); err != nil {
		t.Fatal(err)
	}
	var err error
	h.session, err = h.s.CreateSession(t.Context(), h.tenant, store.CreateSessionInput{Creator: store.FixtureCreator(), Engine: "fixture_harness", IdempotencyKey: "structured", Configuration: configuration})
	if err != nil {
		t.Fatal(err)
	}
	if err = h.s.BindSessionDevice(t.Context(), h.tenant, h.session.ID, h.device.ID); err != nil {
		t.Fatal(err)
	}
	caps := proto.AgentKindCapabilities{Streaming: true, Steering: true, DurableTurns: true, DurableInputReceipts: true, ExecutionControls: true, EnvironmentNone: true, SubagentControl: true, ToolObservations: true, StructuredOutput: true, MessageItems: true}
	h.write("", proto.TypeHeartbeat, proto.HeartbeatPayload{SupportedAgentKinds: []proto.SupportedAgentKind{{Kind: "fixture_harness", Available: true, Capabilities: caps}}})
	peer, _ := h.registry.LookupDevice(h.device.ID)
	for deadline := time.Now().Add(3 * time.Second); ; {
		info, found, known := peer.AgentKindStatus("fixture_harness")
		if known && found && info.Capabilities.StructuredOutput {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("capability heartbeat missing")
		}
		time.Sleep(10 * time.Millisecond)
	}
	input := h.message("start", "Run")
	// A restart can remove qualification while admitted work remains queued.
	// Runtime advertisements cannot qualify an operation on their own.
	profile.StructuredOutput = false
	h.d.Policy = execution.Policy{Engines: engine.NewCatalog(map[string]engine.Profile{"fixture_harness": profile})}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if result := <-h.run(ctx, input.TurnID); result.err == nil {
		t.Fatal("unqualified structured output dispatched")
	}
	turn, err := h.s.GetTurn(t.Context(), h.tenant, h.session.ID, input.TurnID)
	if err != nil || turn.Status != store.TurnQueued {
		t.Fatal("unqualified work was claimed", turn.Status, err)
	}
}
