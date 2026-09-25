package execution

import (
	"context"
	"errors"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/observability"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

type toolCapture struct{ attempts []observability.ToolAttempt }

func (c *toolCapture) Record(value observability.ToolAttempt) { c.attempts = append(c.attempts, value) }

type toolEventWriter struct{ err error }

func (w *toolEventWriter) AppendTurnEvents(_ context.Context, _, _, _ string, _ int32, _ []store.ExecutionEvent) error {
	return w.err
}

func TestToolAttemptProjectsOnlyTerminalSanitizedEvidence(t *testing.T) {
	capture := &toolCapture{}
	writer := &toolEventWriter{}
	j := &journal{store: writer, tenant: "tenant", session: "session", turn: "turn", toolRecorder: capture}
	before, err := proto.NewEnvelope(proto.TypeToolCall, "turn", proto.ToolCallPayload{
		ID: "native-call", Stage: "before", Name: "secret-tool-name",
		Observation: &proto.ToolObservation{Kind: "command", Status: "in_progress", Command: "secret command"},
	})
	if err != nil || j.enqueue(before) != nil || len(capture.attempts) != 0 {
		t.Fatalf("tool start should not count as terminal: %v", err)
	}
	after, err := proto.NewEnvelope(proto.TypeToolCall, "turn", proto.ToolCallPayload{
		ID: "native-call", Stage: "after", Name: "secret-tool-name",
		Observation: &proto.ToolObservation{Kind: "command", Status: "failed", Command: "secret command"},
	})
	if err != nil || j.enqueue(after) != nil || len(capture.attempts) != 0 {
		t.Fatalf("tool attempt must wait for durable event append: %v", err)
	}
	writer.err = errors.New("event persistence failed")
	if j.flush(context.Background()) == nil || len(capture.attempts) != 0 {
		t.Fatal("failed event append produced a tool metric")
	}
	writer.err = nil
	if err := j.flush(context.Background()); err != nil || len(capture.attempts) != 1 {
		t.Fatalf("durable terminal tool observation missing: %v", err)
	}
	got := capture.attempts[0]
	if got.Category != "command" || got.Outcome != "error" || got.StartedAt == nil || got.DurationMS == nil ||
		got.ID == "native-call" || got.ID == "secret-tool-name" || got.TurnID != "turn" {
		t.Fatalf("tool attempt lost its bounded projection: %+v", got)
	}
	if err := j.enqueue(after); err != nil {
		t.Fatal(err)
	}
	if err := j.flush(context.Background()); err != nil || capture.attempts[1].ID != got.ID {
		t.Fatalf("retry did not retain deterministic attempt identity: %v", err)
	}
}
