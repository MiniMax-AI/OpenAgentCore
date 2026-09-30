package store

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/google/uuid"
)

func TestSubagentNativeFunctionResultDoesNotConsumeOutputIndex(t *testing.T) {
	s, pool := testStore(t)
	owner := executionWriter(t, s)
	tenant, session := newSubagentSession(t, s)
	host, err := s.CreateDevice(t.Context(), tenant, "child outputs", runtimedevice.HashCredential(uuid.NewString()))
	if err != nil {
		t.Fatal(err)
	}
	if err = owner.BindSessionDevice(t.Context(), tenant, session.ID, host.ID); err != nil {
		t.Fatal(err)
	}
	input := submitMessage(t, s, tenant, session.ID, "start")
	transition(t, owner, tenant, session.ID, input.TurnID, sessions.TurnQueued, sessions.TurnInProgress)
	call := json.RawMessage(`{"id":"native-file-change","stage":"after","observation":{"status":"completed","kind":"function","name":"apply_patch","arguments":{"count":9007199254740993,"scale":1e2},"content":[{"type":"input_text","text":"file written"}]}}`)
	text := "child answer"
	message, _ := json.Marshal(proto.OutputMessagePayload{ID: "answer", Status: "completed", Text: &text})
	facts := []ExecutionEvent{
		subagentIdentityEvent("child", "root", 100),
		subagentFact(proto.TypeSubagentTurn, proto.SubagentTurnPayload{NativeID: "child", TurnID: "turn", Status: sessions.TurnInProgress, CreatedAtMS: 100000}),
		subagentFact(proto.TypeSubagentItem, proto.SubagentItemPayload{NativeID: "child", TurnID: "turn", ItemID: "native-file-change", Position: 0, Kind: proto.TypeToolCall, Payload: call}),
		subagentFact(proto.TypeSubagentItem, proto.SubagentItemPayload{NativeID: "child", TurnID: "turn", ItemID: "answer", Position: 1, Kind: proto.TypeOutputMessage, Payload: message}),
	}
	if err = owner.AppendTurnEvents(t.Context(), tenant, session.ID, input.TurnID, 1, facts); err != nil {
		t.Fatal(err)
	}
	finished := int64(101000)
	terminal := subagentFact(proto.TypeSubagentTurn, proto.SubagentTurnPayload{NativeID: "child", TurnID: "turn", Status: sessions.TurnCompleted, CreatedAtMS: 100000, CompletedAtMS: &finished})
	if err = owner.AppendTurnEvents(t.Context(), tenant, session.ID, input.TurnID, 5, []ExecutionEvent{terminal, facts[2], facts[3]}); err != nil {
		t.Fatal("identical native tool history must survive replay after completion", err)
	}
	child, err := s.GetSubagentIdentity(t.Context(), tenant, session.ID, "child")
	if err != nil {
		t.Fatal(err)
	}
	items, err := s.ListSubagentItems(t.Context(), tenant, session.ID, child.ID, "", 20, true)
	if err != nil || len(items.Data) != 3 {
		t.Fatal(items, err)
	}
	// Child Items publish no Session events; only root work reaches the stream.
	events, err := s.ListSessionEvents(t.Context(), tenant, session.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range events {
		e := change.Event
		if (e.TurnID != "" && e.TurnID != input.TurnID) || (e.Item != nil && e.Item.TurnID != input.TurnID) {
			t.Fatal("child work on the Session stream", e.Type)
		}
	}
	rows, err := pool.Query(t.Context(), `SELECT payload->>'type', output_index FROM subagent_items WHERE session_id = $1`, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	found := map[string]*int32{}
	for rows.Next() {
		var kind string
		var index *int32
		if err := rows.Scan(&kind, &index); err != nil {
			t.Fatal(err)
		}
		found[kind] = index
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(found) != 3 || found["function_call_output"] != nil {
		t.Fatal("native tool result consumed output index", found)
	}
	if call, message := found["function_call"], found["message"]; call == nil || *call != 0 || message == nil || *message != 1 {
		t.Fatal(found)
	}
}

func TestSubagentCancelledPartialMessageSurvivesHistoryReplay(t *testing.T) {
	s, _ := testStore(t)
	owner := executionWriter(t, s)
	tenant, session := newSubagentSession(t, s)
	host, err := s.CreateDevice(t.Context(), tenant, "cancelled child", runtimedevice.HashCredential(uuid.NewString()))
	if err != nil {
		t.Fatal(err)
	}
	if err = owner.BindSessionDevice(t.Context(), tenant, session.ID, host.ID); err != nil {
		t.Fatal(err)
	}
	input := submitMessage(t, s, tenant, session.ID, "start")
	transition(t, owner, tenant, session.ID, input.TurnID, sessions.TurnQueued, sessions.TurnInProgress)
	message := subagentFact(proto.TypeSubagentItem, proto.SubagentItemPayload{
		NativeID: "child", TurnID: "child-turn", ItemID: "partial", Kind: proto.TypeOutputMessage,
		Payload: json.RawMessage(`{"id":"partial","status":"incomplete","text":"Partial native answer"}`),
	})
	finished := int64(101000)
	terminal := subagentFact(proto.TypeSubagentTurn, proto.SubagentTurnPayload{NativeID: "child", TurnID: "child-turn", Status: sessions.TurnCancelled, CreatedAtMS: 100000, CompletedAtMS: &finished})
	facts := []ExecutionEvent{
		subagentIdentityEvent("child", "root", 100),
		subagentFact(proto.TypeSubagentTurn, proto.SubagentTurnPayload{NativeID: "child", TurnID: "child-turn", Status: sessions.TurnInProgress, CreatedAtMS: 100000}),
		message, terminal,
		// A cold history read must preserve the partial answer without re-execution.
		message, terminal,
	}
	if err := owner.AppendTurnEvents(t.Context(), tenant, session.ID, input.TurnID, 1, facts); err != nil {
		t.Fatal(err)
	}
	child, err := s.GetSubagentIdentity(t.Context(), tenant, session.ID, "child")
	if err != nil {
		t.Fatal(err)
	}
	items, err := s.ListSubagentItems(t.Context(), tenant, session.ID, child.ID, "", 20, true)
	if err != nil || len(items.Data) != 1 || items.Data[0].Status != "incomplete" || *items.Data[0].Content[0].Text != "Partial native answer" {
		t.Fatal(items, err)
	}
	turns, err := s.ListSubagentTurns(t.Context(), tenant, session.ID, child.ID, "", 20, true)
	if err != nil || len(turns.Data) != 1 || turns.Data[0].Status != sessions.TurnCancelled {
		t.Fatal(turns, err)
	}
}

func TestSubagentRootCompletionRetainsNativeSourceTime(t *testing.T) {
	for _, status := range []string{sessions.TurnCompleted, sessions.TurnCancelled} {
		t.Run(status, func(t *testing.T) {
			s, _ := testStore(t)
			tenant, session := newTurnSession(t, s)
			input := submitMessage(t, s, tenant, session.ID, "start")
			transition(t, s, tenant, session.ID, input.TurnID, sessions.TurnQueued, sessions.TurnInProgress)
			current, err := s.GetTurn(t.Context(), tenant, session.ID, input.TurnID)
			if err != nil {
				t.Fatal(err)
			}
			source := current.CreatedAt.Unix() * 1000
			outcome := json.RawMessage(fmt.Sprintf(`{"done":{"source_completed_at_ms":%d}}`, source))
			completed, err := s.CompleteExecution(t.Context(), tenant, session.ID, input.TurnID, status, outcome, "", input.Sequence)
			if err != nil {
				t.Fatal(err)
			}
			if status == sessions.TurnCompleted && !completed.CompletedAt.Equal(time.UnixMilli(source)) {
				t.Fatal("child drain changed root source completion", completed.CompletedAt)
			}
			if status == sessions.TurnCancelled && !completed.CompletedAt.After(time.UnixMilli(source)) {
				t.Fatal("cancellation reused native success timestamp", completed.CompletedAt)
			}
		})
	}
}
