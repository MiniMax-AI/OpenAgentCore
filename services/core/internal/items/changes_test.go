package items

import (
	"encoding/json"
	"testing"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
)

func TestObserveDecidesItemChanges(t *testing.T) {
	project := func(kind, body string) Update {
		t.Helper()
		updates, err := Project(testTurn, kind, 1, []byte(body))
		if err != nil || len(updates) == 0 {
			t.Fatal(updates, err)
		}
		return updates[len(updates)-1]
	}
	draft := project("delta", `{"item_id":"a","delta":"draft"}`).Item
	command := project("tool_call", `{"id":"c","stage":"before","observation":{"status":"in_progress","kind":"command","command":"ls"}}`).Item
	result := project("tool_call", `{"id":"f","stage":"after","observation":{"status":"completed","kind":"function","name":"lookup","content":[{"type":"input_text","text":"native"}]}}`)
	for _, test := range []struct {
		name   string
		kind   string
		update Update
		stored Stored
		check  func(Change) bool
	}{
		{
			name: "a text delta merges and keeps its own fragment", kind: "delta",
			update: project("delta", `{"item_id":"a","delta":" more"}`), stored: Stored{Item: draft},
			check: func(c Change) bool {
				return *c.Item.Content[0].Text == "draft more" && *c.Delta == " more" && *c.Previous.Content[0].Text == "draft"
			},
		},
		{
			name: "command output carries its fragment", kind: "command_output",
			update: project("command_output", `{"id":"c","delta":"out"}`), stored: Stored{Item: command},
			check: func(c Change) bool { return *c.Delta == "out" && c.Item.Output == "out" && c.Output },
		},
		{
			name: "a function result keeps the saved submission and takes no output index", kind: "tool_call",
			update: result, stored: Stored{FunctionResult: json.RawMessage(`{"output":"saved","error":null}`)},
			check: func(c Change) bool {
				return string(c.Item.Output.(json.RawMessage)) == `"saved"` && string(c.Item.Error.(json.RawMessage)) == "null" && !c.Output
			},
		},
		{
			name: "an input message takes no output index", kind: "message",
			update: project("message", `{"type":"agent.session.input.message","input":[{"role":"user","content":[{"type":"input_text","text":"hello"}]}]}`),
			check:  func(c Change) bool { return !c.Output && c.Item.Role == "user" },
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			change, err := Observe(test.kind, test.update, test.stored)
			if err != nil {
				t.Fatal(err)
			}
			if !test.check(change) {
				t.Fatalf("%+v", change)
			}
		})
	}
}

func TestUpdatesDeclareTheFactsObserveReads(t *testing.T) {
	message := Update{Item: v1.Item{Type: "message"}}
	result := Update{Item: v1.Item{Type: "function_call_output", CallID: "call"}}
	if call, ok := result.ResultCall(); !ok || call != "call" {
		t.Fatal("result call", call, ok)
	}
	if _, ok := message.ResultCall(); ok {
		t.Fatal("message asked for a function result")
	}
}
