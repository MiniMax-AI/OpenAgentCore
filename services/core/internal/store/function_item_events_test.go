package store

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/items"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

func TestFunctionResultEventsAreInputs(t *testing.T) {
	s, _ := testStore(t)
	tenant, session := newTurnSession(t, s)
	turn := submitMessage(t, s, tenant, session.ID, "start").TurnID
	transition(t, s, tenant, session.ID, turn, sessions.TurnQueued, sessions.TurnInProgress)
	events := []sessions.ExecutionEvent{
		{Kind: "tool_call", Payload: json.RawMessage(`{"id":"call","stage":"after","observation":{"status":"completed","kind":"function","name":"lookup","arguments":{},"content":[{"type":"input_text","text":"result"}]}}`)},
		{Kind: "delta", Payload: json.RawMessage(`{"item_id":"answer","delta":"answer"}`)},
	}
	if err := sessionExecution(t, executionWriter(t, s).lease).AppendTurnEvents(t.Context(), tenant, session.ID, turn, 1, events); err != nil {
		t.Fatal(err)
	}
	changes, err := s.ListSessionEvents(t.Context(), tenant, session.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	results := 0
	for _, change := range changes {
		event := change.Event
		if event.Item == nil {
			continue
		}
		if event.Item.Type == "function_call_output" {
			results++
			if event.Type != "agent.session.turn.item.added" || event.OutputIndex != nil {
				t.Fatal("function result is not agent output", event)
			}
		}
		if event.Item.Type == "message" && event.Item.Role == "assistant" && (event.OutputIndex == nil || *event.OutputIndex != 1) {
			t.Fatal("function result consumed an output index", event)
		}
	}
	page, err := sessionAdapter(s).ListItems(t.Context(), tenant, session.ID, "", 100, true)
	if err != nil || results != 1 {
		t.Fatal(page, results, err)
	}
	found := false
	for _, item := range page.Items {
		if item.Type == "function_call_output" {
			found = true
		}
	}
	if !found {
		t.Fatal("function result missing from recovery items")
	}
}

func TestFunctionResultItemsRetainSubmittedFields(t *testing.T) {
	for _, raw := range []string{
		`{"success":true}`,
		`{"success":false,"output":null,"error":null}`,
		`{"success":true,"output":"original"}`,
		`{"success":false,"output":[{"type":"input_text","text":"before"},{"type":"input_image","image_url":"data:image/png;base64,AA=="}],"error":"failure"}`,
	} {
		t.Run(raw, func(t *testing.T) {
			s, pool := testStore(t)
			functions := functionExecution(t)
			tenant, session := newTurnSession(t, s)
			turn := submitMessage(t, s, tenant, session.ID, "start").TurnID
			transition(t, s, tenant, session.ID, turn, sessions.TurnQueued, sessions.TurnInProgress)
			call := functionCallFixture(items.Identity(turn, "tool:call"))
			if err := functions.RecordFunctionCall(t.Context(), tenant, session.ID, turn, call); err != nil {
				t.Fatal(err)
			}
			if err := SubmitFixtureFunctionResult(t.Context(), s, tenant, session.ID, turn, call.CallID, json.RawMessage(raw)); err != nil {
				t.Fatal(err)
			}
			event := sessions.ExecutionEvent{Kind: "tool_call", Payload: json.RawMessage(`{"id":"call","stage":"after","observation":{"status":"completed","kind":"function","name":"lookup","arguments":{},"content":[{"type":"input_text","text":"normalized"}]}}`)}
			if err := functions.AppendTurnEvents(t.Context(), tenant, session.ID, turn, 1, []sessions.ExecutionEvent{event}); err != nil {
				t.Fatal(err)
			}
			assertFields := func(value any) {
				t.Helper()
				encoded, err := json.Marshal(value)
				if err != nil {
					t.Fatal(err)
				}
				var expected, actual map[string]any
				if json.Unmarshal([]byte(raw), &expected) != nil || json.Unmarshal(encoded, &actual) != nil {
					t.Fatal(string(encoded))
				}
				// The wire always carries both fields, null when not submitted (EVT-09).
				for _, field := range []string{"output", "error"} {
					got, exists := actual[field]
					if !exists || !reflect.DeepEqual(expected[field], got) {
						t.Fatalf("%s changed: %s", field, encoded)
					}
				}
			}
			page, err := sessionAdapter(s).ListItems(t.Context(), tenant, session.ID, "", 100, true)
			if err != nil {
				t.Fatal(err)
			}
			results := 0
			for _, item := range page.Items {
				if item.Type == "function_call_output" {
					assertFields(item)
					results++
				}
			}
			changes, err := s.ListSessionEvents(t.Context(), tenant, session.ID, 0)
			if err != nil {
				t.Fatal(err)
			}
			for _, change := range changes {
				if change.Event.Item != nil && change.Event.Item.Type == "function_call_output" {
					assertFields(change.Event.Item)
					results++
				}
			}
			// The stored payload keeps the submitted field presence.
			var stored map[string]any
			if err := pool.QueryRow(t.Context(), `SELECT payload FROM session_items WHERE turn_id = $1 AND payload->>'type' = 'function_call_output'`, turn).Scan(&stored); err != nil {
				t.Fatal(err)
			}
			var submitted map[string]any
			_ = json.Unmarshal([]byte(raw), &submitted)
			for _, field := range []string{"output", "error"} {
				_, present := submitted[field]
				if _, exists := stored[field]; present != exists {
					t.Fatalf("stored %s presence changed: %v", field, stored)
				}
			}
			if results != 2 {
				t.Fatal("missing saved or streamed result", results)
			}
		})
	}
}
