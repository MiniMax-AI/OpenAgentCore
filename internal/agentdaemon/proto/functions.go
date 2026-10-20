package proto

import (
	"encoding/json"
	"errors"
)

const (
	TypeFunctionCall          = "function_call"
	TypeFunctionCallWithdrawn = "function_call_withdrawn"
	TypeFunctionResult        = "function_result"
)

type FunctionTool struct {
	Name         string          `json:"name"`
	Description  string          `json:"description"`
	Parameters   json.RawMessage `json:"parameters"`
	DeferLoading bool            `json:"defer_loading,omitempty"`
}

// ValidateToolSearch checks only the requested function discovery operation.
// Native search configuration and discovery stay inside the adapter.
func (r PromptRequestPayload) ValidateToolSearch(supported bool) error {
	deferred := false
	for _, tool := range r.FunctionTools {
		deferred = deferred || tool.DeferLoading
	}
	if !r.ToolSearch && !deferred {
		return nil
	}
	if !supported {
		return errors.New("engine does not support deferred function discovery")
	}
	if !r.ToolSearch || !deferred {
		return errors.New("qualified discovery requires tool search and deferred functions")
	}
	return nil
}

// FunctionCallPayload belongs to the Run identified by Envelope.ID. NativeID
// and TurnID name the native child and its Turn when a Subagent made the call,
// with the meaning they have in SubagentTurnPayload; both are empty for a call
// of the Run itself.
type FunctionCallPayload struct {
	CallID    string          `json:"call_id"`
	NativeID  string          `json:"native_id,omitempty"`
	TurnID    string          `json:"turn_id,omitempty"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// FunctionCallWithdrawnPayload tells that the native Harness will never apply a
// result for the call, which CallID, NativeID and TurnID identify as in
// FunctionCallPayload.
type FunctionCallWithdrawnPayload struct {
	CallID   string `json:"call_id"`
	NativeID string `json:"native_id,omitempty"`
	TurnID   string `json:"turn_id,omitempty"`
}

type FunctionResultPayload struct {
	DeliveryID string         `json:"delivery_id"`
	CallID     string         `json:"call_id"`
	Success    bool           `json:"success"`
	Content    []InputContent `json:"content"`
}

func (r FunctionResultPayload) ValidateContent() error {
	if r.Content == nil {
		return errors.New("function result requires a content array")
	}
	for _, part := range r.Content {
		if err := part.Validate(); err != nil {
			return err
		}
	}
	return nil
}
