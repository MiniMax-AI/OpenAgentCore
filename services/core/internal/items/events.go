package items

import (
	"reflect"
	"strings"

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
		if item.Type == "reasoning" {
			// Reasoning text arrives only through summary events.
			initial.Status, initial.Summary = "in_progress", []v1.SummaryText{}
		}
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
	if item.Type == "reasoning" {
		// Each summary part is framed like assistant text. A part is done once
		// a later part starts or the Item ends.
		done := func(summary []v1.SummaryText, status string) int {
			if status == "in_progress" && len(summary) > 0 {
				return len(summary) - 1
			}
			return len(summary)
		}
		closed := 0
		if previous.ID != "" {
			closed = done(previous.Summary, previous.Status)
		}
		for i, part := range item.Summary {
			index, before := int64(i), ""
			event := base
			event.ItemID, event.SummaryIndex = item.ID, &index
			if i < len(previous.Summary) {
				before = previous.Summary[i].Text
			} else {
				empty := ""
				event.Part = &v1.ItemContent{Type: "summary_text", Text: &empty}
				emit("reasoning_summary_part.added", event)
				event.Part = nil
			}
			// A child snapshot that rewrote earlier text reports it only when done.
			if fragment, ok := strings.CutPrefix(part.Text, before); ok && fragment != "" {
				event.Delta = &fragment
				emit("reasoning_summary_text.delta", event)
				event.Delta = nil
			}
			if i >= closed && i < done(item.Summary, item.Status) {
				text := part.Text
				event.Text = &text
				emit("reasoning_summary_text.done", event)
				event.Text, event.Part = nil, &v1.ItemContent{Type: "summary_text", Text: &text}
				if item.Status == "incomplete" {
					status := "incomplete"
					event.Status = &status
				}
				emit("reasoning_summary_part.done", event)
			}
		}
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
