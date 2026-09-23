package v1

import (
	"encoding/json"
	"reflect"
	"testing"
)

func assertJSONEqual(t *testing.T, got []byte, want string) {
	t.Helper()
	var actual, expected any
	if err := json.Unmarshal(got, &actual); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(want), &expected); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("wire shape:\n got %s\nwant %s", got, want)
	}
}

func TestSubagentWireShapePreservesNullableMetadata(t *testing.T) {
	for _, raw := range []string{
		`{"id":"child","object":"agent.session.subagent","session_id":"session","parent_agent_id":"root","opened_at":1700000000,"closed_at":null,"name":null,"instructions":null,"status":"active"}`,
		`{"id":"nested","object":"agent.session.subagent","session_id":"session","parent_agent_id":"child","opened_at":1700000001,"closed_at":1700000002,"name":"reviewer","instructions":[{"type":"output_text","text":"Review."},{"type":"encrypted_content","encrypted_content":"opaque"}],"status":"closed"}`,
	} {
		var value Subagent
		if err := json.Unmarshal([]byte(raw), &value); err != nil {
			t.Fatal(err)
		}
		body, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		assertJSONEqual(t, body, raw)
	}
}

func TestTurnWireShapeIncludesNullableSubagentIdentity(t *testing.T) {
	turn := Turn{ID: "turn", AgentID: "root", SessionID: "session", Object: "agent.session.turn", Status: "completed", CreatedAt: 1700000000}
	body, err := json.Marshal(turn)
	if err != nil {
		t.Fatal(err)
	}
	assertJSONEqual(t, body, `{"id":"turn","agent_id":"root","subagent_id":null,"session_id":"session","object":"agent.session.turn","status":"completed","created_at":1700000000,"started_at":null,"completed_at":null,"error":null,"usage":null}`)
	// A child Turn keeps the Session's Agent ID; subagent_id identifies the child.
	child := "child"
	turn.SubagentID = &child
	body, err = json.Marshal(turn)
	if err != nil {
		t.Fatal(err)
	}
	var returned Turn
	if err := json.Unmarshal(body, &returned); err != nil || returned.SubagentID == nil || *returned.SubagentID != child || returned.AgentID != "root" {
		t.Fatalf("child identity: %s (%v)", body, err)
	}
}

func TestSubagentListUsesCommonEnvelope(t *testing.T) {
	first := "child"
	body, err := json.Marshal(SubagentList{Object: "list", FirstID: &first, LastID: &first, Data: []Subagent{{ID: first, Object: "agent.session.subagent", SessionID: "session", ParentAgentID: "root", OpenedAt: 1700000000, Status: "active"}}})
	if err != nil {
		t.Fatal(err)
	}
	assertJSONEqual(t, body, `{"object":"list","first_id":"child","last_id":"child","has_more":false,"data":[{"id":"child","object":"agent.session.subagent","session_id":"session","parent_agent_id":"root","opened_at":1700000000,"closed_at":null,"name":null,"instructions":null,"status":"active"}]}`)
	body, err = json.Marshal(SubagentList{Object: "list", Data: []Subagent{}})
	if err != nil {
		t.Fatal(err)
	}
	assertJSONEqual(t, body, `{"object":"list","first_id":null,"last_id":null,"data":[],"has_more":false}`)
}

func TestCoordinationItemsMatchPinnedWireShapes(t *testing.T) {
	fixtures := []string{
		`{"id":"create","turn_id":"turn","type":"create_subagent_call","status":"completed","agent_id":"root","content":[],"model":null,"reasoning_effort":null}`,
		`{"id":"create-model","turn_id":"turn","type":"create_subagent_call","status":"failed","agent_id":"root","content":[{"type":"output_text","text":""}],"model":"requested-model","reasoning_effort":"high"}`,
		`{"id":"send","turn_id":"turn","type":"send_subagent_input_call","status":"completed","sender_agent_id":"root","recipient_agent_id":"child","content":[{"type":"encrypted_content","encrypted_content":"opaque"}]}`,
		`{"id":"wait","turn_id":"turn","type":"wait_for_subagents_call","status":"in_progress","sender_agent_id":"root","recipient_agent_ids":["child","other"]}`,
		`{"id":"resume","turn_id":"turn","type":"resume_subagent_call","status":"completed","sender_agent_id":"root","recipient_agent_id":"child"}`,
		`{"id":"interrupt","turn_id":"turn","type":"interrupt_subagent_call","status":"incomplete","sender_agent_id":"root","recipient_agent_id":"child"}`,
		`{"id":"close","turn_id":"turn","type":"close_subagent_call","status":"failed","sender_agent_id":"root","recipient_agent_id":"child"}`,
		`{"id":"message","turn_id":"turn","type":"agent_message","sender_agent_id":"child","recipient_agent_id":"root","content":[{"type":"output_text","text":"Result."}]}`,
		`{"id":"reasoning","turn_id":"turn","type":"reasoning","status":null,"summary":[]}`,
		`{"id":"reasoning","turn_id":"turn","type":"reasoning","status":"completed","summary":[{"type":"summary_text","text":"Checked."}]}`,
	}
	for _, raw := range fixtures {
		var item Item
		if err := json.Unmarshal([]byte(raw), &item); err != nil {
			t.Fatal(err)
		}
		// Irrelevant fields from other variants must not leak into public coordination Items.
		item.Command, item.Name = "private command", "native name"
		if item.Type == "agent_message" {
			item.Status = "completed"
		}
		body, err := json.Marshal(item)
		if err != nil {
			t.Fatal(err)
		}
		assertJSONEqual(t, body, raw)
	}
}

func TestCoordinationItemsDoNotEncodeInputContentAsAgentContent(t *testing.T) {
	text := "instruction"
	for _, content := range []ItemContent{
		{Type: "input_text", Text: &text},
		{Type: "output_text"},
		{Type: "encrypted_content"},
	} {
		if _, err := json.Marshal(Item{Type: "agent_message", Content: []ItemContent{content}}); err == nil {
			t.Fatalf("accepted invalid content: %+v", content)
		}
	}
}
