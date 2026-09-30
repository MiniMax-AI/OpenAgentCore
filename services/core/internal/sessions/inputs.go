package sessions

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Input is a validated execution command, not an upstream wire type.
// The API validates event fields before constructing this storage input.
type Input struct {
	Kind    string          `json:"kind"`
	Payload json.RawMessage `json:"payload"`
}

type InputReceipt struct {
	Sequence int64
	TurnID   string // Empty for a cancellation accepted while the Session was idle.
	Replayed bool
}

type TurnInput struct {
	Sequence  int64
	Kind      string
	Payload   json.RawMessage
	CreatedAt time.Time
}

// ValidateInputKey enforces the shared request identity limit, including no-op requests.
func ValidateInputKey(key string) error {
	if strings.TrimSpace(key) == "" || len(key) > 128 {
		return fmt.Errorf("%w: idempotency key is required and limited to 128 bytes", ErrInvalidInput)
	}
	return nil
}

// FunctionCall retains public identity and its opaque execution-adapter reference.
type FunctionCall struct {
	CallID, ExecutorCallID, Name string
	Arguments                    json.RawMessage
	Result                       json.RawMessage
	Applied                      bool
}

// FunctionResultInput identifies a persisted call; Result is validated by the API.
// It is an internal command, not an upstream input event. TurnID is the caller's
// value and is resolved within the Session at admission.
type FunctionResultInput struct {
	TurnID string          `json:"turn_id"`
	CallID string          `json:"call_id"`
	Result json.RawMessage `json:"result"`
}
