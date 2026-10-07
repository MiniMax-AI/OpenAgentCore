package v1

import (
	"bytes"
	"encoding/json"
	"errors"
)

// MarshalJSON preserves required null function arguments without exposing function fields on connection actions.
func (a RequiredAction) MarshalJSON() ([]byte, error) {
	switch a.Type {
	case "function_call":
		return json.Marshal(FunctionCallAction{Type: a.Type, Arguments: a.Arguments, CallID: a.CallID, Name: a.Name, TurnID: a.TurnID})
	case "environment_connection":
		return json.Marshal(EnvironmentConnectionAction{Type: a.Type, EnvironmentID: a.EnvironmentID})
	default:
		return nil, errors.New("unsupported required action type")
	}
}

// UnmarshalJSON preserves function argument integer precision.
func (a *RequiredAction) UnmarshalJSON(raw []byte) error {
	type wire RequiredAction
	var value wire
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	*a = RequiredAction(value)
	return nil
}
