package proto

// Preparation controls use a caller request ID on Envelope.ID, never a RunID.
// Handles are daemon-generated and valid only on the accepting connection.
const (
	TypeExecutionPrepare  = "execution_prepare"
	TypeExecutionStart    = "execution_start"
	TypeExecutionRelease  = "execution_release"
	TypePreparationStatus = "preparation_status"
)

// ExecutionPreparePayload reuses execution configuration without accepting
// input. SessionID identifies the immutable configuration owner; each request
// reserves a separate Turn admission on its Runtime Executor.
type ExecutionPreparePayload struct {
	SessionID     string               `json:"session_id"`
	Configuration PromptRequestPayload `json:"configuration"`
}

type ExecutionStartPayload struct {
	ExecutorID string       `json:"executor_id"`
	Handle     string       `json:"handle"`
	RunID      string       `json:"run_id"`
	Input      MessageInput `json:"input"`
}

type ExecutionReleasePayload struct {
	Handle string `json:"handle"`
}

// PreparationStatusPayload is a connection-local observation, not a public event.
// Keep the highest Revision for each Handle; concurrent sends may arrive out of
// order. ExpiresAt is Unix milliseconds. Repeated requests do not extend it. Retired request IDs
// may allocate a fresh handle, but an old handle can never start its replacement.
type PreparationStatusPayload struct {
	// ExecutorID identifies the native resource owner, independently of this admission handle.
	ExecutorID string `json:"executor_id,omitempty"`
	// Reused is true when readiness reused an existing settled Executor.
	Reused    bool   `json:"reused,omitempty"`
	Handle    string `json:"handle,omitempty"`
	Revision  uint64 `json:"revision"`
	State     string `json:"state"`
	ExpiresAt int64  `json:"expires_at,omitempty"`
	RunID     string `json:"run_id,omitempty"`
	ErrorCode string `json:"error_code,omitempty"`
	// Operation is supplied for a rejected control operation (no resource revision).
	Operation string `json:"operation,omitempty"`
}
