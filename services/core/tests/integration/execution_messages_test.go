package integration

import (
	"context"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

// TestExecutionProjectsIdentifiedMessages checks that each assistant message
// becomes the Item its identity names: the completion snapshot replaces its
// deltas, and a message the Turn leaves open ends incomplete when the Turn is
// cancelled or fails.
func TestExecutionProjectsIdentifiedMessages(t *testing.T) {
	for _, end := range []string{sessions.TurnCancelled, sessions.TurnFailed} {
		t.Run(end, func(t *testing.T) {
			h := newDispatchHarness(t)
			ctx := context.Background()
			input := h.message("observed", "stream separate messages")
			result := h.run(ctx, input.TurnID)
			h.read(testExecutionRequest)
			text := "complete"
			h.write(input.TurnID, proto.TypeOutputMessage, proto.OutputMessagePayload{ID: "a", Status: "in_progress", Phase: "commentary"})
			h.write(input.TurnID, proto.TypeDelta, proto.DeltaPayload{ItemID: "a", Delta: "draft", Sequence: 1})
			h.write(input.TurnID, proto.TypeOutputMessage, proto.OutputMessagePayload{ID: "a", Status: "completed", Phase: "commentary", Text: &text})
			h.write(input.TurnID, proto.TypeOutputMessage, proto.OutputMessagePayload{ID: "b", Status: "in_progress", Phase: "final_answer"})
			h.write(input.TurnID, proto.TypeDelta, proto.DeltaPayload{ItemID: "b", Delta: "partial", Sequence: 2})
			if end == sessions.TurnCancelled {
				if _, err := requestCancel(ctx, h.s, h.tenant, h.session.ID, "cancel"); err != nil {
					t.Fatal(err)
				}
				var cancel proto.PromptCancelPayload
				_ = h.read(proto.TypePromptCancel).DecodePayload(&cancel)
				h.write(input.TurnID, proto.TypeInteractionDecisionAck, proto.InteractionDecisionAckPayload{DeliveryID: cancel.DeliveryID, Applied: true, Outcome: &proto.DonePayload{}})
			} else {
				h.write(input.TurnID, proto.TypeError, proto.ErrorPayload{Error: "Engine failed"})
				h.write(input.TurnID, proto.TypeDone, proto.DonePayload{})
			}
			h.finished(result, end)
			page, err := sessionAdapter(h.s).ListItems(ctx, h.tenant, h.session.ID, "", 100, true)
			if err != nil || len(page.Items) != 3 {
				t.Fatal(page, err)
			}
			for i, want := range []struct{ status, phase, text string }{{"completed", "commentary", "complete"}, {"incomplete", "final_answer", "partial"}} {
				item := page.Items[i+1]
				if item.Type != "message" || item.Role != "assistant" || item.Status != want.status || item.Phase != want.phase || len(item.Content) != 1 || item.Content[0].Text == nil || *item.Content[0].Text != want.text {
					t.Fatalf("message %d: %+v", i, item)
				}
			}
		})
	}
}
