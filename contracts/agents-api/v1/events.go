package v1

import "encoding/json"

// SessionEvent contains the supported live event variants of the pinned protocol.
type SessionEvent struct {
	Subagent     *Subagent                `json:"subagent,omitempty"`
	Type         string                   `json:"type" binding:"required"`
	EventID      string                   `json:"event_id" binding:"required"`
	SessionID    string                   `json:"session_id,omitempty"`
	TurnID       string                   `json:"turn_id,omitempty"`
	Session      *Session                 `json:"session,omitempty"`
	Turn         *Turn                    `json:"turn,omitempty"`
	Item         *Item                    `json:"item,omitempty"`
	ItemID       string                   `json:"item_id,omitempty"`
	OutputIndex  *int32                   `json:"output_index,omitempty" extensions:"x-nullable"`
	ContentIndex *int                     `json:"content_index,omitempty"`
	Part         *ItemContent             `json:"part,omitempty"`
	Delta        *string                  `json:"delta,omitempty"`
	Text         *string                  `json:"text,omitempty"`
	Error        *StreamError             `json:"error,omitempty"`
	Environment  *SessionEnvironmentState `json:"environment,omitempty"`
	// Usage is present only on terminal Turn events, where it mirrors the Turn
	// snapshot and is null when unknown. Other events omit it.
	Usage *TokenUsage `json:"usage,omitempty" extensions:"x-nullable"`
}

type StreamError struct {
	Code    string `json:"code"`
	Type    string `json:"type"`
	Message string `json:"message"`
}

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
// and a nullable output_index on every Item event (EVT-09).
func (e SessionEvent) MarshalJSON() ([]byte, error) {
	type wire SessionEvent
	switch {
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
