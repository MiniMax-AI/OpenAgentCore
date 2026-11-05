package integration

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig"
	"github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig/codex"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/execution"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

// A Session update reaches the Turns whose input is accepted after it. A Turn
// queued before keeps its settings, and a change the Harness does not declare
// is refused without a write.
func TestSessionUpdateBindsModelToLaterTurns(t *testing.T) {
	h := newDispatchHarness(t)
	ctx := context.Background()
	policy := func(model proto.CapabilitySupport) execution.Policy {
		declared := codex.Declaration.Clone()
		declared.SessionUpdates.Model = model
		return execution.Policy{Declarations: harnessconfig.NewRegistry(declared)}
	}
	update := func(policy execution.Policy, model string) error {
		_, err := sessionService(t, h.s).UpdateSession(ctx, sessions.UpdateSessionCommand{TenantID: h.tenant, SessionID: h.session.ID, Configure: func(engine string, stored json.RawMessage) (json.RawMessage, error) {
			var configuration map[string]any
			_ = json.Unmarshal(stored, &configuration)
			configuration["agent"].(map[string]any)["model"] = model
			updated, _ := json.Marshal(configuration)
			return updated, policy.ValidateSessionUpdate(engine, stored, updated)
		}})
		return err
	}
	prepared := func(turn string) proto.PromptRequestPayload {
		result := h.run(ctx, turn)
		var prompt proto.PromptRequestPayload
		_ = h.read(testExecutionRequest).DecodePayload(&prompt)
		h.write(turn, proto.TypeDone, proto.DonePayload{Content: "Done"})
		h.finished(result, sessions.TurnCompleted)
		return prompt
	}
	first := h.message("first", "Before the update")
	if update(policy(proto.CapabilityUnsupported), "refused-model") == nil {
		t.Fatal("a change the Harness does not declare was accepted")
	}
	if err := update(policy(proto.CapabilitySupported), "later-model"); err != nil {
		t.Fatal(err)
	}
	if got := prepared(first.TurnID).Turn.Model; got != "test-model" {
		t.Fatalf("the queued Turn ran with model %q", got)
	}
	second := h.message("second", "After the update")
	if got := prepared(second.TurnID).Turn.Model; got != "later-model" {
		t.Fatalf("the later Turn ran with model %q", got)
	}
}
