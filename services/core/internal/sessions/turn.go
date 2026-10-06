package sessions

import (
	"encoding/json"
	"time"
)

const (
	TurnQueued     = "queued"
	TurnInProgress = "in_progress"
	TurnWaiting    = "waiting"
	TurnCompleted  = "completed"
	TurnFailed     = "failed"
	TurnCancelled  = "cancelled"
)

// Turn is a root Turn from the Core work queue and uses its Session's immutable
// execution configuration; Subagent Turns have a native writer and their own
// table. Zero timestamps mean the corresponding event has not occurred. Outcome
// is adapter-owned data, not an upstream response; the API must project
// supported wire types explicitly.
type Turn struct {
	ID, SessionID, Status string
	CreatedAt             time.Time
	StartedAt             time.Time
	CompletedAt           time.Time
	CancelRequestedAt     time.Time
	Usage                 json.RawMessage
	Outcome               json.RawMessage
	// ArtifactCaptureStarted is private Runtime coordination, never a wire field.
	ArtifactCaptureStarted bool `json:"-"`
}

// TerminalStatus reports whether a Turn status has ended the Turn. A terminal
// Turn is never reopened and its outcome is never overwritten.
func TerminalStatus(status string) bool {
	return status == TurnCompleted || status == TurnFailed || status == TurnCancelled
}

// ValidTransition reports whether a Turn may move from one status to another:
// a queued Turn starts, fails or is cancelled, and an in-progress or waiting
// Turn moves between the two or ends. An ended Turn never moves.
func ValidTransition(from, to string) bool {
	switch from {
	case TurnQueued:
		return to == TurnInProgress || to == TurnFailed || to == TurnCancelled
	case TurnInProgress:
		return to == TurnWaiting || TerminalStatus(to)
	case TurnWaiting:
		return to == TurnInProgress || TerminalStatus(to)
	default:
		return false
	}
}

type TurnTransition struct {
	ExpectedStatus string
	Status         string
	Outcome        json.RawMessage
}

type TurnPage struct {
	Turns      []Turn
	NextCursor string
}

type TurnEvent struct {
	Ordinal   int32
	Kind      string
	Payload   json.RawMessage
	CreatedAt time.Time
}

type ExecutionEvent struct {
	Kind    string          `json:"kind"`
	Payload json.RawMessage `json:"payload"`
}

type ExecutionWork struct{ TenantID, SessionID, TurnID, Status string }

// ExecutionDevice contains safe identity only, never a device credential.
type ExecutionDevice struct {
	ID            string
	Name          string
	EnvironmentID string
}

// ExecutionBinding identifies the Runtime and native history selected for one API Session.
type ExecutionBinding struct {
	Device          ExecutionDevice
	NativeSessionID string
	HasStartedTurn  bool
}
