package items

import (
	"reflect"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
)

// Events returns the public events that report change, in publication order.
// outputIndex is the Item's output index, nil when it has none. An unchanged
// Item reports nothing.
func Events(change Change, outputIndex *int32) []v1.SessionEvent {
	previous, item, delta := change.Previous, change.Item, change.Delta
	if reflect.DeepEqual(previous, item) {
		return nil
	}
	// Function results are Session inputs, not AgentOutputItem variants.
	if item.Type == "function_call_output" {
		outputIndex = nil
	}
	base := v1.SessionEvent{TurnID: item.TurnID}
	if outputIndex != nil {
		index := *outputIndex
		base.OutputIndex = &index
	}
	var events []v1.SessionEvent
	emit := func(kind string, event v1.SessionEvent) {
		event.Type = "agent.session.turn." + kind
		events = append(events, event)
	}
	textMessage := item.Type == "message" && item.Role == "assistant" && len(item.Content) == 1 && item.Content[0].Text != nil
	if previous.ID == "" {
		initial := item
		if textMessage {
			// Assistant text is added empty and in progress; its text arrives
			// only through deltas, as in official streams (EVT-10).
			initial.Status, initial.Content = "in_progress", []v1.ItemContent{}
		}
		event := base
		event.Item = &initial
		emit("item.added", event)
		if textMessage {
			zero, empty := 0, ""
			event = base
			event.ItemID, event.ContentIndex, event.Part = item.ID, &zero, &v1.ItemContent{Type: item.Content[0].Type, Text: &empty}
			emit("content_part.added", event)
			if delta == nil {
				// A first observation without its own fragment, such as a
				// non-streamed native final, carries its unchanged text in one
				// delta. This frames the text; it never alters it.
				delta = item.Content[0].Text
			}
		}
	}
	if outputIndex == nil {
		return events
	}
	if item.Type == "command_execution" && delta != nil {
		event := base
		event.Type = "agent.output.command_execution_output.delta"
		event.ItemID, event.Delta = item.ID, delta
		return append(events, event)
	}
	if textMessage {
		zero := 0
		event := base
		event.ItemID, event.ContentIndex = item.ID, &zero
		if delta != nil && *delta != "" {
			event.Delta = delta
			emit("output_text.delta", event)
			event.Delta = nil
		}
		if item.Status != "in_progress" {
			event.Text = item.Content[0].Text
			emit("output_text.done", event)
			event.Text, event.Part = nil, &item.Content[0]
			emit("content_part.done", event)
		}
	}
	if item.Status != "in_progress" {
		event := base
		event.Item = &item
		emit("item.done", event)
	}
	return events
}
