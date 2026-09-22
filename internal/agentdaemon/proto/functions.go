package proto

import (
	"encoding/json"
	"errors"
)

const (
	TypeFunctionCall   = "function_call"
	TypeFunctionResult = "function_result"
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

// FunctionCallPayload belongs to the Run identified by Envelope.ID.
type FunctionCallPayload struct {
	CallID    string          `json:"call_id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
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
