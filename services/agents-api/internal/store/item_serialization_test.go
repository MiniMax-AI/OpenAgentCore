package store

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

type wireEvent struct {
	kind   string
	fields map[string]json.RawMessage
	item   map[string]json.RawMessage
}

// wireEvents renders recorded Session events as clients receive them.
func wireEvents(t *testing.T, s *Store, tenant, session string, after int64) []wireEvent {
	t.Helper()
	changes, err := s.ListSessionEvents(t.Context(), tenant, session, after)
	if err != nil {
		t.Fatal(err)
	}
	var result []wireEvent
	for _, change := range changes {
		raw, err := json.Marshal(change.Event)
		if err != nil {
			t.Fatal(err)
		}
		event := wireEvent{kind: strings.TrimPrefix(change.Event.Type, "agent.session.")}
		if err := json.Unmarshal(raw, &event.fields); err != nil {
			t.Fatal(err)
		}
		if item := event.fields["item"]; item != nil {
			if err := json.Unmarshal(item, &event.item); err != nil {
				t.Fatal(err)
			}
		}
		result = append(result, event)
	}
	return result
}

func TestAssistantMessageEventsFollowOfficialSequence(t *testing.T) {
	// Non-ASCII text, escape sequences and surrounding whitespace stay unchanged.
	answer := " {\"text\":\"red é \\u00e9\\n\"}\n"
	for _, test := range []struct {
		name   string
		events []ExecutionEvent
		phase  string
		deltas []string
		final  string
	}{
		{"streamed deltas", []ExecutionEvent{
			{Kind: "delta", Payload: json.RawMessage(`{"item_id":"a","delta":"Hel"}`)},
			{Kind: "delta", Payload: json.RawMessage(`{"item_id":"a","delta":"lo"}`)},
			{Kind: "output_message", Payload: json.RawMessage(`{"id":"a","status":"completed","text":"Hello"}`)},
		}, "null", []string{"Hel", "lo"}, "Hello"},
		{"native start", []ExecutionEvent{
			{Kind: "output_message", Payload: json.RawMessage(`{"id":"a","status":"in_progress","phase":"final_answer"}`)},
			{Kind: "delta", Payload: json.RawMessage(`{"item_id":"a","delta":"Hi"}`)},
			{Kind: "output_message", Payload: json.RawMessage(`{"id":"a","status":"completed","phase":"final_answer","text":"Hi"}`)},
		}, `"final_answer"`, []string{"Hi"}, "Hi"},
		// A non-streamed native final carries its exact text in one delta.
		{"non-streamed final", []ExecutionEvent{
			{Kind: "output_message", Payload: json.RawMessage(`{"id":"a","status":"completed","phase":"final_answer","text":` + mustJSON(t, answer) + `}`)},
		}, `"final_answer"`, []string{answer}, answer},
	} {
		t.Run(test.name, func(t *testing.T) {
			s, _ := testStore(t)
			tenant, session := newTurnSession(t, s)
			turn := submitMessage(t, s, tenant, session.ID, "start").TurnID
			transition(t, s, tenant, session.ID, turn, TurnQueued, TurnInProgress)
			cursor, err := s.SessionEventCursor(t.Context(), tenant, session.ID)
			if err != nil {
				t.Fatal(err)
			}
			if err := s.AppendTurnEvents(t.Context(), tenant, session.ID, turn, 1, test.events); err != nil {
				t.Fatal(err)
			}
			var kinds, deltas []string
			var done string
			for _, event := range wireEvents(t, s, tenant, session.ID, cursor) {
				kinds = append(kinds, event.kind)
				if string(event.fields["output_index"]) != "0" {
					t.Fatalf("%s output_index = %s", event.kind, event.fields["output_index"])
				}
				switch event.kind {
				case "turn.item.added":
					if string(event.item["status"]) != `"in_progress"` || string(event.item["content"]) != "[]" || string(event.item["phase"]) != test.phase {
						t.Fatalf("item.added: %s", event.fields["item"])
					}
				case "turn.content_part.added":
					if string(event.fields["part"]) != `{"type":"output_text","text":""}` {
						t.Fatalf("content_part.added: %s", event.fields["part"])
					}
				case "turn.output_text.delta":
					var delta string
					_ = json.Unmarshal(event.fields["delta"], &delta)
					deltas = append(deltas, delta)
				case "turn.output_text.done":
					_ = json.Unmarshal(event.fields["text"], &done)
				case "turn.item.done":
					var content []map[string]string
					if json.Unmarshal(event.item["content"], &content) != nil || len(content) != 1 || content[0]["text"] != test.final || string(event.item["status"]) != `"completed"` || string(event.item["phase"]) != test.phase {
						t.Fatalf("item.done: %s", event.fields["item"])
					}
				}
			}
			want := []string{"turn.item.added", "turn.content_part.added"}
			for range test.deltas {
				want = append(want, "turn.output_text.delta")
			}
			want = append(want, "turn.output_text.done", "turn.content_part.done", "turn.item.done")
			if !reflect.DeepEqual(kinds, want) || !reflect.DeepEqual(deltas, test.deltas) || done != test.final {
				t.Fatalf("sequence %v deltas %q done %q", kinds, deltas, done)
			}
			page, err := s.ListItems(t.Context(), tenant, session.ID, "", 100, true)
			if err != nil || len(page.Items) != 2 || *page.Items[1].Content[0].Text != test.final {
				t.Fatalf("stored answer changed: %+v %v", page, err)
			}
		})
	}
}

func TestInputItemEventsCarryNullOutputIndexAndPhase(t *testing.T) {
	s, _ := testStore(t)
	tenant, session := newTurnSession(t, s)
	cursor, err := s.SessionEventCursor(t.Context(), tenant, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	submitMessage(t, s, tenant, session.ID, "start")
	added := 0
	for _, event := range wireEvents(t, s, tenant, session.ID, cursor) {
		if event.kind != "turn.item.added" {
			if _, present := event.fields["output_index"]; present {
				t.Fatalf("%s carries output_index", event.kind)
			}
			continue
		}
		added++
		if index, present := event.fields["output_index"]; !present || string(index) != "null" || string(event.item["phase"]) != "null" || string(event.item["role"]) != `"user"` {
			t.Fatalf("user item.added: %v %s", event.fields, event.fields["item"])
		}
	}
	page, err := s.ListItems(t.Context(), tenant, session.ID, "", 100, true)
	if err != nil || added != 1 || len(page.Items) != 1 {
		t.Fatal(page, added, err)
	}
	listed, err := json.Marshal(page.Items[0])
	if err != nil || !strings.Contains(string(listed), `"phase":null`) {
		t.Fatalf("listed user message: %s %v", listed, err)
	}
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
