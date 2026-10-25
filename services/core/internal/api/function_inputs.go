package api

import (
	"bytes"
	"encoding/json"
	"reflect"
	"slices"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

type decodedInputEvent struct {
	v1.SessionInput
	Output json.RawMessage `json:"output,omitempty"`
}

func decodeInputEvent(raw json.RawMessage) (decodedInputEvent, error) {
	var event decodedInputEvent
	if err := json.Unmarshal(raw, &event); err != nil {
		return event, sessions.ErrInvalidInput
	}
	fields := []string{"type"}
	switch event.Type {
	case "agent.session.input.message":
		fields = append(fields, "input")
	case "agent.session.input.cancel":
	case "agent.session.input.tool_result":
		fields = append(fields, "call_id", "turn_id", "success", "error", "output")
	default:
		return event, sessions.ErrInvalidInput
	}
	return event, decodeInputObject(raw, &event, fields...)
}

func decodeInputObject(raw json.RawMessage, value any, allowed ...string) error {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return sessions.ErrInvalidInput
	}
	for field := range fields {
		if !slices.Contains(allowed, field) {
			return sessions.ErrInvalidInput
		}
	}
	// Nested members match exactly too; see inexactMember. The raw value is
	// valid JSON here, as Unmarshal accepted it.
	if inexactMember(raw, reflect.TypeOf(value)) {
		return sessions.ErrInvalidInput
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(value) != nil {
		return sessions.ErrInvalidInput
	}
	return nil
}

func functionResultInput(event decodedInputEvent) (sessions.Input, error) {
	if event.CallID == "" || event.TurnID == "" || event.Success == nil {
		return sessions.Input{}, sessions.ErrInvalidInput
	}
	result, err := json.Marshal(struct {
		Success bool            `json:"success"`
		Error   json.RawMessage `json:"error,omitempty"`
		Output  json.RawMessage `json:"output,omitempty"`
	}{*event.Success, event.Error, event.Output})
	if err != nil {
		return sessions.Input{}, err
	}
	payload, err := json.Marshal(sessions.FunctionResultInput{TurnID: event.TurnID, CallID: event.CallID, Result: result})
	return sessions.Input{Kind: "tool_result", Payload: payload}, err
}
