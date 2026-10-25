package items

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
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

func TestReasoningPartsBecomeOneItemWithOfficialEvents(t *testing.T) {
	index := int32(0)
	var stored v1.Item
	var kinds, deltas []string
	for _, frame := range []proto.ReasoningPayload{
		{ID: "r1", Status: "in_progress", Delta: "think "},
		{ID: "r1", Status: "in_progress", Delta: "hard"},
		{ID: "r1", Status: "in_progress", Index: 1, Delta: "then act"},
		{ID: "r1", Status: "completed"},
	} {
		raw, _ := json.Marshal(frame)
		updates, err := Project(testTurn, proto.TypeReasoning, 1, raw)
		if err != nil || len(updates) != 1 {
			t.Fatalf("project %+v: %v %v", frame, updates, err)
		}
		change, ok, err := Observe(proto.TypeReasoning, updates[0], Stored{Item: stored})
		if err != nil || !ok {
			t.Fatalf("observe %+v: %v %v", frame, ok, err)
		}
		for _, event := range Events(change, &index) {
			kind := event.Type[len("agent.session.turn."):]
			if event.SummaryIndex != nil {
				kind += fmt.Sprint(*event.SummaryIndex)
			}
			kinds = append(kinds, kind)
			if event.Delta != nil {
				deltas = append(deltas, *event.Delta)
			}
		}
		stored = change.Item
	}
	wantKinds := []string{"item.added", "reasoning_summary_part.added0", "reasoning_summary_text.delta0", "reasoning_summary_text.delta0",
		"reasoning_summary_text.done0", "reasoning_summary_part.done0", "reasoning_summary_part.added1", "reasoning_summary_text.delta1",
		"reasoning_summary_text.done1", "reasoning_summary_part.done1", "item.done"}
	if !reflect.DeepEqual(kinds, wantKinds) || !reflect.DeepEqual(deltas, []string{"think ", "hard", "then act"}) {
		t.Fatalf("events %q deltas %q", kinds, deltas)
	}
	want := []v1.SummaryText{{Type: "summary_text", Text: "think hard"}, {Type: "summary_text", Text: "then act"}}
	if stored.Type != "reasoning" || stored.Status != "completed" || !reflect.DeepEqual(stored.Summary, want) {
		t.Fatalf("item %+v", stored)
	}
}

func TestOpenReasoningEndsIncompleteWithItsText(t *testing.T) {
	open := v1.Item{ID: "item", TurnID: testTurn, Type: "reasoning", Status: "in_progress", Summary: []v1.SummaryText{{Type: "summary_text", Text: "partial"}}}
	ended := open
	ended.Status = "incomplete"
	index := int32(0)
	var kinds []string
	for _, event := range Events(Change{Previous: open, Item: ended}, &index) {
		kinds = append(kinds, event.Type[len("agent.session.turn."):])
		if event.Type == "agent.session.turn.reasoning_summary_part.done" && (event.Status == nil || *event.Status != "incomplete" || *event.Part.Text != "partial") {
			t.Fatalf("part done %+v", event)
		}
	}
	if want := []string{"reasoning_summary_text.done", "reasoning_summary_part.done", "item.done"}; !reflect.DeepEqual(kinds, want) {
		t.Fatalf("events %q, want %q", kinds, want)
	}
}
