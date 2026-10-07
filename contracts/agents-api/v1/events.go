package v1

import "encoding/json"

// TerminalTurnEvent reports whether an event type settles a Turn.
func TerminalTurnEvent(eventType string) bool {
	switch eventType {
	case "agent.session.turn.completed", "agent.session.turn.failed", "agent.session.turn.cancelled":
		return true
	}
	return false
}

// itemEvent reports whether an event type adds or completes an Item.
func itemEvent(eventType string) bool {
	return eventType == "agent.session.turn.item.added" || eventType == "agent.session.turn.item.done"
}

// MarshalJSON keeps the nullable top-level usage on terminal Turn events only,
// a nullable output_index on every Item event (EVT-09) and a nullable error
// param on error events.
func (e SessionEvent) MarshalJSON() ([]byte, error) {
	type wire SessionEvent
	switch {
	case e.Type == "error" && e.Error != nil:
		e.Usage = nil
		return json.Marshal(struct {
			wire
			Error sessionError `json:"error"`
		}{wire(e), sessionError(*e.Error)})
	case TerminalTurnEvent(e.Type):
		return json.Marshal(struct {
			wire
			Usage *TokenUsage `json:"usage"`
		}{wire(e), e.Usage})
	case itemEvent(e.Type):
		e.Usage = nil
		return json.Marshal(struct {
			wire
			OutputIndex *int32 `json:"output_index"`
		}{wire(e), e.OutputIndex})
	}
	e.Usage = nil
	return json.Marshal(wire(e))
}
