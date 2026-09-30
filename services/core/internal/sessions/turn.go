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
