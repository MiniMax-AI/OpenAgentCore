package store

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/device"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/google/uuid"
)

func newSubagentSession(t *testing.T, s *Store) (string, Session) {
	t.Helper()
	tenant := uuid.NewString()
	session, err := s.CreateSession(t.Context(), tenant, CreateSessionInput{Creator: FixtureCreator(), Engine: "codex", IdempotencyKey: "subagent", Configuration: json.RawMessage(`{"agent":{"id":"agent_root","model":"test","multi_agent":{"enabled":true,"max_concurrent_subagents":6}}}`)})
	if err != nil {
		t.Fatal(err)
	}
	return tenant, session
}
func subagentFact(kind string, value any) ExecutionEvent {
	raw, _ := json.Marshal(value)
	return ExecutionEvent{Kind: kind, Payload: raw}
}
func TestSubagentResourcesNativeOwnershipLifecycleAndRecovery(t *testing.T) {
	s, pool := testStore(t)
	owner := executionLease(t, s).Store()
	ctx := t.Context()
	tenant, session := newSubagentSession(t, s)
	host, err := s.CreateDevice(ctx, tenant, "child resources", device.HashCredential(uuid.NewString()))
	if err != nil {
		t.Fatal(err)
	}
	if err = owner.BindSessionDevice(ctx, tenant, session.ID, host.ID); err != nil {
		t.Fatal(err)
	}
	root := submitMessage(t, s, tenant, session.ID, "first")
	transition(t, owner, tenant, session.ID, root.TurnID, TurnQueued, TurnInProgress)
	ordinal := int32(1)
	appendFacts := func(facts ...ExecutionEvent) {
		t.Helper()
		if err := owner.AppendTurnEvents(ctx, tenant, session.ID, root.TurnID, ordinal, facts); err != nil {
			t.Fatal(err)
		}
		ordinal += int32(len(facts))
	}
	appendFacts(subagentIdentityEvent("child", "root", 100), subagentIdentityEvent("nested", "child", 101), subagentIdentityEvent("sibling", "root", 100))
	page, err := s.ListSubagents(ctx, tenant, session.ID, "", 100, true)
	if err != nil || len(page.Data) != 3 {
		t.Fatal(page, err)
	}
	child, err := s.GetSubagentIdentity(ctx, tenant, session.ID, "child")
	if err != nil {
		t.Fatal(err)
	}
	nested, _ := s.GetSubagentIdentity(ctx, tenant, session.ID, "nested")
	resource, err := s.GetSubagent(ctx, tenant, session.ID, nested.ID)
	if err != nil || resource.ParentAgentID != child.ID {
		t.Fatal(resource, err)
	}
	opened := int64(101000)
	finished := int64(102000)
	turn := proto.SubagentTurnPayload{NativeID: "child", TurnID: "native-turn-1", Status: TurnInProgress, CreatedAtMS: opened, StartedAtMS: &opened}
	appendFacts(subagentFact(proto.TypeSubagentTurn, turn))
	text := "child-owned-result"
	message, _ := json.Marshal(proto.OutputMessagePayload{ID: "native-message", Status: "completed", Text: &text})
	item := proto.SubagentItemPayload{NativeID: "child", TurnID: turn.TurnID, ItemID: "native-message", Position: 0, Kind: proto.TypeOutputMessage, Payload: message}
	appendFacts(subagentFact(proto.TypeSubagentItem, item))
	turn.Status = TurnCompleted
	turn.CompletedAtMS = &finished
	appendFacts(subagentFact(proto.TypeSubagentTurn, turn))
	// Later history reads preserve the terminal Turn and all existing Items.
	appendFacts(subagentFact(proto.TypeSubagentItem, item), subagentFact(proto.TypeSubagentTurn, turn))
	turns, err := s.ListSubagentTurns(ctx, tenant, session.ID, child.ID, "", 20, true)
	if err != nil || len(turns.Data) != 1 {
		t.Fatal(turns, err)
	}
	tid := turns.Data[0].ID
	// agent_id is the Session's Agent ID; subagent_id identifies the child.
	if turns.Data[0].AgentID != "agent_root" || turns.Data[0].SubagentID == nil || *turns.Data[0].SubagentID != child.ID || turns.Data[0].Usage != nil {
		t.Fatal(turns)
	}
	if same, err := s.GetSubagentTurn(ctx, tenant, session.ID, child.ID, tid); err != nil || !reflect.DeepEqual(same, turns.Data[0]) {
		t.Fatal(same, err)
	}
	// Session Turn reads carry root work only: a child Turn ID is missing there.
	if _, err = s.GetTurn(ctx, tenant, session.ID, tid); !errors.Is(err, ErrNotFound) {
		t.Fatal("child Turn in Session Turn retrieval", err)
	}
	if _, err = s.ListTurns(ctx, tenant, session.ID, tid, 100, true); !errors.Is(err, ErrNotFound) {
		t.Fatal("child Turn as a Session Turn cursor", err)
	}
	allTurns, err := s.ListTurns(ctx, tenant, session.ID, "", 100, true)
	if err != nil || len(allTurns.Turns) != 1 || allTurns.Turns[0].ID != root.TurnID {
		t.Fatal(allTurns, err)
	}
	// Nested children follow the same agent_id rule (unobserved officially).
	nestedTurn := proto.SubagentTurnPayload{NativeID: "nested", TurnID: "native-nested-turn", Status: TurnInProgress, CreatedAtMS: opened, StartedAtMS: &opened}
	appendFacts(subagentFact(proto.TypeSubagentTurn, nestedTurn))
	nestedTurns, err := s.ListSubagentTurns(ctx, tenant, session.ID, nested.ID, "", 20, true)
	if err != nil || len(nestedTurns.Data) != 1 || nestedTurns.Data[0].AgentID != "agent_root" || *nestedTurns.Data[0].SubagentID != nested.ID {
		t.Fatal(nestedTurns, err)
	}
	if _, err = s.GetSubagentTurn(ctx, uuid.NewString(), session.ID, child.ID, tid); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign tenant child Turn", err)
	}
	if _, err = s.ListSubagentTurns(ctx, uuid.NewString(), session.ID, child.ID, "", 20, true); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign tenant child Turns", err)
	}
	if allTurns, err = s.ListTurns(ctx, tenant, session.ID, "", 100, true); err != nil || len(allTurns.Turns) != 1 {
		t.Fatal(allTurns, err)
	}
	own, err := s.ListSubagentTurnItems(ctx, tenant, session.ID, child.ID, tid, "", 20, true)
	if err != nil || len(own.Data) != 1 || own.Data[0].TurnID != tid {
		t.Fatal(own, err)
	}
	rootItems, err := s.ListItems(ctx, tenant, session.ID, "", 100, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range rootItems.Items {
		if entry.ID == own.Data[0].ID {
			t.Fatal("child Item leaked into root Items")
		}
	}
	if _, err = s.GetSubagentTurn(ctx, tenant, session.ID, nested.ID, tid); !errors.Is(err, ErrNotFound) {
		t.Fatal("nested ownership", err)
	}
	if _, err = s.ListSubagentItems(ctx, tenant, session.ID, nested.ID, own.Data[0].ID, 20, true); !errors.Is(err, errItemCursor) {
		t.Fatal("another child's Item cursor", err)
	}
	if _, err = s.GetSubagent(ctx, uuid.NewString(), session.ID, child.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign tenant", err)
	}
	closed := proto.SubagentLifecyclePayload{NativeID: "child", EffectID: "native-close-1", Status: "closed", OccurredAtMS: 103000}
	resumed := proto.SubagentLifecyclePayload{NativeID: "child", EffectID: "native-resume-1", Status: "active", OccurredAtMS: 104000}
	appendFacts(subagentFact(proto.TypeSubagentLifecycle, closed), subagentFact(proto.TypeSubagentLifecycle, closed))
	value, err := s.GetSubagent(ctx, tenant, session.ID, child.ID)
	if err != nil || value.Status != "closed" || value.ClosedAt == nil || *value.ClosedAt != 103 {
		t.Fatal(value, err)
	}
	appendFacts(subagentFact(proto.TypeSubagentLifecycle, resumed), subagentFact(proto.TypeSubagentLifecycle, resumed))
	reopened := New(pool)
	value, err = reopened.GetSubagent(ctx, tenant, session.ID, child.ID)
	if err != nil || value.Status != "active" || value.ClosedAt != nil || value.OpenedAt != 100 {
		t.Fatal(value, err)
	}
	changes, err := s.ListSessionEvents(ctx, tenant, session.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, change := range changes {
		if change.Event.Subagent != nil && change.Event.Subagent.ID == child.ID {
			counts[change.Event.Type]++
		}
		// Child Turns and their Items publish no Session events.
		if strings.HasPrefix(change.Event.Type, "agent.session.turn.") && change.Event.Turn != nil && change.Event.Turn.SubagentID != nil {
			t.Fatal("child Turn on the Session stream", change.Event.Type)
		}
		if (change.Event.TurnID != "" && change.Event.TurnID != root.TurnID) || (change.Event.Item != nil && change.Event.Item.TurnID != root.TurnID) {
			t.Fatal("child work on the Session stream", change.Event.Type)
		}
		if change.Turn != nil && change.Turn.ID != root.TurnID {
			t.Fatal("child Turn snapshot on the Session stream", change.Event.Type)
		}
	}
	for _, kind := range []string{"created", "closed", "active"} {
		if counts["agent.session.subagent."+kind] != 1 {
			t.Fatal(counts)
		}
	}
	// A conflicting replay rolls back the whole batch, including an earlier new child.
	closed.OccurredAtMS++
	facts := []ExecutionEvent{subagentIdentityEvent("rollback", "root", 104), subagentFact(proto.TypeSubagentLifecycle, closed)}
	if err = owner.AppendTurnEvents(ctx, tenant, session.ID, root.TurnID, ordinal, facts); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatal(err)
	}
	if _, err = s.GetSubagentIdentity(ctx, tenant, session.ID, "rollback"); !errors.Is(err, ErrNotFound) {
		t.Fatal("non-atomic batch", err)
	}
	// Reads do not invoke native processes, including after the root finishes.
	transition(t, owner, tenant, session.ID, root.TurnID, TurnInProgress, TurnCompleted)
	if _, err = reopened.GetSubagentTurn(context.Background(), tenant, session.ID, child.ID, tid); err != nil {
		t.Fatal(err)
	}
}
