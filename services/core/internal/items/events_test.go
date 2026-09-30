package items

import (
	"encoding/json"
	"reflect"
	"testing"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
)

func assistantText(status, text string) v1.Item {
	return v1.Item{ID: "item", TurnID: testTurn, Type: "message", Role: "assistant", Status: status, Content: []v1.ItemContent{{Type: "output_text", Text: &text}}}
}

func TestEventsReportItemChangesInOrder(t *testing.T) {
	index := int32(2)
	fragment := " more"
	command := v1.Item{ID: "item", TurnID: testTurn, Type: "command_execution", Status: "in_progress", Command: "ls"}
	commandOutput := command
	commandOutput.Output = fragment
	result := v1.Item{ID: "item", TurnID: testTurn, Type: "function_call_output", CallID: "call", Status: "completed", Output: json.RawMessage(`"ok"`)}
	user := v1.Item{ID: "item", TurnID: testTurn, Type: "message", Role: "user", Status: "completed", Content: []v1.ItemContent{{Type: "input_text"}}}
	tool := v1.Item{ID: "item", TurnID: testTurn, Type: "web_search_call", Status: "in_progress"}
	doneTool := tool
	doneTool.Status = "completed"
	for _, test := range []struct {
		name   string
		change Change
		index  *int32
		want   []string
		deltas []string
	}{
		{
			name:   "a new final text is framed by one delta",
			change: Change{Item: assistantText("completed", "answer")}, index: &index,
			want:   []string{"item.added", "content_part.added", "output_text.delta", "output_text.done", "content_part.done", "item.done"},
			deltas: []string{"answer"},
		},
		{
			name:   "a new streaming text carries its own fragment",
			change: Change{Item: assistantText("in_progress", "draft"), Delta: &fragment}, index: &index,
			want:   []string{"item.added", "content_part.added", "output_text.delta"},
			deltas: []string{fragment},
		},
		{
			name:   "a text update publishes only its fragment",
			change: Change{Previous: assistantText("in_progress", "draft"), Item: assistantText("in_progress", "draft more"), Delta: &fragment}, index: &index,
			want:   []string{"output_text.delta"},
			deltas: []string{fragment},
		},
		{
			name:   "an unfinished text that ends is done without a delta",
			change: Change{Previous: assistantText("in_progress", "partial"), Item: assistantText("incomplete", "partial")}, index: &index,
			want: []string{"output_text.done", "content_part.done", "item.done"},
		},
		{
			name:   "an unchanged Item reports nothing",
			change: Change{Previous: assistantText("completed", "same"), Item: assistantText("completed", "same")}, index: &index,
		},
		{
			name:   "command output is a command delta",
			change: Change{Previous: command, Item: commandOutput, Delta: &fragment}, index: &index,
			want:   []string{"agent.output.command_execution_output.delta"},
			deltas: []string{fragment},
		},
		{
			name:   "a function result is an input without an index or item.done",
			change: Change{Item: result}, index: &index,
			want: []string{"item.added"},
		},
		{
			name:   "an input without an output index is only added",
			change: Change{Item: user},
			want:   []string{"item.added"},
		},
		{
			name:   "a tool call that ends is done",
			change: Change{Previous: tool, Item: doneTool}, index: &index,
			want: []string{"item.done"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			events := Events(test.change, test.index)
			var kinds, deltas []string
			for _, event := range events {
				kind := event.Type
				if kind != "agent.output.command_execution_output.delta" {
					kind = kind[len("agent.session.turn."):]
				}
				kinds = append(kinds, kind)
				if event.Delta != nil {
					deltas = append(deltas, *event.Delta)
				}
				if event.TurnID != testTurn {
					t.Fatalf("%s lost its Turn: %+v", kind, event)
				}
				wantIndex := test.index != nil && test.change.Item.Type != "function_call_output"
				if (event.OutputIndex != nil) != wantIndex || (wantIndex && *event.OutputIndex != index) {
					t.Fatalf("%s output index %v", kind, event.OutputIndex)
				}
			}
			if !reflect.DeepEqual(kinds, test.want) || !reflect.DeepEqual(deltas, test.deltas) {
				t.Fatalf("events %q deltas %q, want %q and %q", kinds, deltas, test.want, test.deltas)
			}
		})
	}
}

func TestEventsAddAssistantTextEmptyAndInProgress(t *testing.T) {
	events := Events(Change{Item: assistantText("completed", "answer")}, nil)
	added := events[0].Item
	if added.Status != "in_progress" || len(added.Content) != 0 || *events[1].Part.Text != "" {
		t.Fatalf("added %+v, part %+v", added, events[1].Part)
	}
}
