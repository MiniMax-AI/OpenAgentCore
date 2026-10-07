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
)

// The static declaration admits the Session; a Runtime whose heartbeat
// narrows the support away never claims its work.
func TestStructuredOutputDispatchChecksTheRuntimeDeclaration(t *testing.T) {
	h := newDispatchHarness(t)
	configuration := json.RawMessage(`{"agent":{"model":"fixture","text":{"format":{"type":"json_schema","schema":{"type":"object"}}}},"environment":{"type":"none"}}`)
	if err := execution.ValidateSessionConfiguration("claude_sdk", configuration); err != nil {
		t.Fatal(err)
	}
	var err error
	h.session, err = h.s.CreateSession(t.Context(), h.tenant, sessions.CreateSession{Creator: FixtureCreator(), Engine: "claude_sdk", IdempotencyKey: "structured", Configuration: configuration})
	if err != nil {
		t.Fatal(err)
	}
	if err = bindSessionDevice(t, h.s, h.tenant, h.session.ID, h.device.ID); err != nil {
		t.Fatal(err)
	}
	caps := prototest.Capabilities(proto.AgentKindCapabilities{EnvironmentNone: proto.CapabilitySupported})
	h.write("", proto.TypeHeartbeat, proto.HeartbeatPayload{HomeRemoval: proto.CapabilityUnsupported, SupportedAgentKinds: []proto.SupportedAgentKind{{Kind: "claude_sdk", Available: true, Capabilities: caps}}})
	peer, _ := h.registry.LookupDevice(h.device.ID)
	for deadline := time.Now().Add(3 * time.Second); ; {
		info, found, known := peer.AgentKindStatus("claude_sdk")
		if known && found && info.Available {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("capability heartbeat missing")
		}
		time.Sleep(10 * time.Millisecond)
	}
	input := h.message("start", "Run")
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if result := <-h.run(ctx, input.TurnID); result.err == nil {
		t.Fatal("structured output dispatched to a Runtime without it")
	}
	turn, err := sessionAdapter(h.s).GetTurn(t.Context(), h.tenant, h.session.ID, input.TurnID)
	if err != nil || turn.Status != sessions.TurnQueued {
		t.Fatal("unsupported work was claimed", turn.Status, err)
	}
}
