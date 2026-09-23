package v1

import (
	"encoding/json"
	"testing"
)

func fields(t *testing.T, value any) map[string]json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]json.RawMessage
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func expectField(t *testing.T, got map[string]json.RawMessage, key, want string) {
	t.Helper()
	value, present := got[key]
	if want == "" {
		if present {
			t.Fatalf("unexpected %s: %s", key, value)
		}
		return
	}
	if !present || string(value) != want {
		t.Fatalf("%s = %s (present %v), want %s", key, value, present, want)
	}
}

func TestItemWireFieldsAreExplicitlyNull(t *testing.T) {
	text, phase := "question", "final_answer"
	user := Item{ID: "user", TurnID: "turn", Type: "message", Status: "completed", Role: "user", Content: []ItemContent{{Type: "input_text", Text: &text}}}
	got := fields(t, user)
	expectField(t, got, "phase", "null")
	expectField(t, got, "content", `[{"type":"input_text","text":"question"}]`)
	expectField(t, got, "error", "")
	expectField(t, got, "output", "")

	assistant := Item{ID: "answer", TurnID: "turn", Type: "message", Status: "in_progress", Role: "assistant", Phase: phase}
	got = fields(t, assistant)
	expectField(t, got, "phase", `"final_answer"`)
	expectField(t, got, "content", "[]")

	for _, test := range []struct {
		name          string
		raw           string
		output, error string
	}{
		{"omitted", `{"id":"r","turn_id":"turn","type":"function_call_output","status":"completed","call_id":"c"}`, "null", "null"},
		{"null", `{"id":"r","turn_id":"turn","type":"function_call_output","status":"failed","call_id":"c","output":null,"error":null}`, "null", "null"},
		{"values", `{"id":"r","turn_id":"turn","type":"function_call_output","status":"failed","call_id":"c","output":[{"type":"input_text","text":"code=9007199254740993"}],"error":"failed"}`, `[{"text":"code=9007199254740993","type":"input_text"}]`, `"failed"`},
	} {
		t.Run(test.name, func(t *testing.T) {
			var item Item
			if err := json.Unmarshal([]byte(test.raw), &item); err != nil {
				t.Fatal(err)
			}
			got := fields(t, item)
			expectField(t, got, "output", test.output)
			expectField(t, got, "error", test.error)
			expectField(t, got, "phase", "")
			// Stored payloads keep the original field presence.
			stored, err := item.MarshalStored()
			if err != nil {
				t.Fatal(err)
			}
			var original, persisted map[string]json.RawMessage
			if json.Unmarshal([]byte(test.raw), &original) != nil || json.Unmarshal(stored, &persisted) != nil || len(original) != len(persisted) {
				t.Fatalf("stored payload changed: %s", stored)
			}
		})
	}

	// Other variants keep omitting unset fields, including the new wire nulls.
	call := fields(t, Item{ID: "call", TurnID: "turn", Type: "function_call", Status: "completed", Name: "lookup", CallID: "call", Arguments: json.RawMessage(`{}`)})
	for _, key := range []string{"phase", "error", "output", "content"} {
		expectField(t, call, key, "")
	}
	reasoning := fields(t, Item{ID: "rs", TurnID: "turn", Type: "reasoning"})
	expectField(t, reasoning, "status", "null")
	expectField(t, reasoning, "summary", "[]")

	stored, err := user.MarshalStored()
	if err != nil {
		t.Fatal(err)
	}
	if string(stored) != `{"id":"user","turn_id":"turn","type":"message","status":"completed","role":"user","content":[{"type":"input_text","text":"question"}]}` {
		t.Fatalf("stored message encoding changed: %s", stored)
	}
}

func TestItemEventsCarryNullableOutputIndex(t *testing.T) {
	text := "question"
	item := &Item{ID: "user", TurnID: "turn", Type: "message", Status: "completed", Role: "user", Content: []ItemContent{{Type: "input_text", Text: &text}}}
	index, zero := int32(2), 0
	for _, test := range []struct {
		event SessionEvent
		want  string
	}{
		{SessionEvent{Type: "agent.session.turn.item.added", Item: item}, "null"},
		{SessionEvent{Type: "agent.session.turn.item.done", Item: item, OutputIndex: &index}, "2"},
		{SessionEvent{Type: "agent.session.turn.content_part.added", ItemID: "answer", OutputIndex: &index, ContentIndex: &zero}, "2"},
		{SessionEvent{Type: "agent.session.turn.created", Turn: &Turn{ID: "turn"}}, ""},
	} {
		test.event.EventID, test.event.TurnID = "event", "turn"
		got := fields(t, test.event)
		expectField(t, got, "output_index", test.want)
		expectField(t, got, "usage", "")
		if test.event.Item != nil {
			expectField(t, fields(t, got["item"]), "phase", "null")
		}
	}
}

func TestReasoningResponsesCarryBothKeys(t *testing.T) {
	low := "low"
	auto := "auto"
	for _, test := range []struct {
		reasoning Reasoning
		want      string
	}{
		{Reasoning{}, `{"effort":null,"summary":null}`},
		{Reasoning{Effort: &low}, `{"effort":"low","summary":null}`},
		{Reasoning{Summary: &auto}, `{"effort":null,"summary":"auto"}`},
	} {
		agent := Agent{ID: "agent", Model: "model", Reasoning: test.reasoning, ServiceTier: "auto", Tools: []json.RawMessage{}}
		session := fields(t, Session{ID: "session", Agent: agent, Object: "agent.session", Metadata: map[string]string{}, RequiredActions: []RequiredAction{}, VaultIDs: []string{}})
		expectField(t, fields(t, session["agent"]), "reasoning", test.want)
		expectField(t, fields(t, session["agent"]), "model", `"model"`)
		expectField(t, session, "usage", "null")
		event := fields(t, SessionEvent{Type: "agent.session.idle", EventID: "event", Session: &Session{Agent: agent}})
		expectField(t, fields(t, fields(t, event["session"])["agent"]), "reasoning", test.want)

		saved := SavedAgent{SavedAgentConfiguration: SavedAgentConfiguration{Model: "model", Reasoning: test.reasoning, ServiceTier: "auto", Tools: []json.RawMessage{}}, ID: "agent", Object: "agent", Metadata: map[string]string{}}
		got := fields(t, saved)
		expectField(t, got, "reasoning", test.want)
		expectField(t, got, "id", `"agent"`)
		expectField(t, got, "model", `"model"`)
		list := fields(t, SavedAgentList{Object: "list", Data: []SavedAgent{saved}})
		var data []map[string]json.RawMessage
		if json.Unmarshal(list["data"], &data) != nil || len(data) != 1 || string(data[0]["reasoning"]) != test.want {
			t.Fatalf("list reasoning: %s", list["data"])
		}
	}
	// Requests and stored configuration keep the omitting encoding.
	stored := fields(t, SavedAgentConfiguration{Model: "model"})
	expectField(t, stored, "reasoning", "{}")
	stored = fields(t, struct {
		Agent Agent `json:"agent"`
	}{Agent{ID: "agent", Reasoning: Reasoning{Effort: &low}}})
	expectField(t, fields(t, stored["agent"]), "reasoning", `{"effort":"low"}`)
}
