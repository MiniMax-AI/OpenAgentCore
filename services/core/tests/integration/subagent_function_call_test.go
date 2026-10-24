package integration

import (
	"encoding/json"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/items"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/google/uuid"
)

func TestSubagentFunctionCallIsPublishedAndAnsweredByChildTurn(t *testing.T) {
	s, _ := testStore(t)
	w := executionWriter(t, s)
	ops := sessionExecution(t, w.lease)
	ctx := t.Context()
	tenant, session := newSubagentSession(t, s)
	host, err := sessionService(t, s).CreateDevice(ctx, tenant, "child call", runtimedevice.HashCredential(uuid.NewString()))
	if err != nil {
		t.Fatal(err)
	}
	if err = ops.BindSessionDevice(ctx, tenant, session.ID, host.ID); err != nil {
		t.Fatal(err)
	}
	root := submitMessage(t, s, tenant, session.ID, "start").TurnID
	transition(t, w, tenant, session.ID, root, sessions.TurnQueued, sessions.TurnInProgress)
	started := int64(101000)
	turn := proto.SubagentTurnPayload{NativeID: "child", TurnID: "native-turn", Status: sessions.TurnInProgress, CreatedAtMS: started, StartedAtMS: &started}
	if err = ops.AppendTurnEvents(ctx, tenant, session.ID, root, 1, []sessions.ExecutionEvent{subagentIdentityEvent("child", "root", 100), subagentFact(proto.TypeSubagentTurn, turn)}); err != nil {
		t.Fatal(err)
	}
	child, err := s.GetSubagentIdentity(ctx, tenant, session.ID, "child")
	if err != nil {
		t.Fatal(err)
	}
	turns, err := sessionAdapter(s).ListSubagentTurns(ctx, tenant, session.ID, child.ID, "", 20, true)
	if err != nil || len(turns.Data) != 1 {
		t.Fatal(turns, err)
	}
	childTurn := turns.Data[0].ID
	before, err := sessionAdapter(s).SessionEventCursor(ctx, tenant, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	call := sessions.FunctionCall{CallID: "ignored", ExecutorCallID: "native-call", Name: "lookup", Arguments: json.RawMessage(`{"ticket":1}`), Subagent: "child", SubagentTurn: "native-turn"}
	for range 2 {
		if err = ops.RecordFunctionCall(ctx, tenant, session.ID, root, call); err != nil {
			t.Fatal(err)
		}
	}
	callID := items.Identity(childTurn, "tool:native-call")
	changes, err := sessionAdapter(s).ListSessionEvents(ctx, tenant, session.ID, before)
	if err != nil {
		t.Fatal(err)
	}
	added := 0
	for _, change := range changes {
		event := change.Event
		if event.Type != "agent.session.turn.item.added" {
			continue
		}
		added++
		item := event.Item
		arguments, _ := json.Marshal(item.Arguments)
		if event.TurnID != childTurn || item.Type != "function_call" || item.ID != callID || item.CallID != callID || item.TurnID != childTurn || item.Name != "lookup" || string(arguments) != `{"ticket":1}` || item.Status != "in_progress" {
			t.Fatal("child function_call event", event)
		}
	}
	if added != 1 {
		t.Fatal("retry published again or nothing", added, changes)
	}
	assertFunctionState(t, s, tenant, session.ID, sessions.TurnWaiting, 1)
	// The official SDK answers with the child Turn ID the event names.
	if err = SubmitFixtureFunctionResult(ctx, s, tenant, session.ID, childTurn, callID, json.RawMessage(`{"success":true,"output":"ok"}`)); err != nil {
		t.Fatal(err)
	}
	if err = ops.ConfirmFunctionResult(ctx, tenant, session.ID, root, callID); err != nil {
		t.Fatal(err)
	}
	assertFunctionState(t, s, tenant, session.ID, sessions.TurnInProgress, 0)
}
