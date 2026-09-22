package proto

// Suspension is private hosted lifecycle traffic. Envelope.ID correlates each
// request; SuspendID fences one persisted suspension attempt across connections.
const (
	TypeEnvironmentQuiesce  = "environment_quiesce"
	TypeEnvironmentQuiesced = "environment_quiesced"
	TypeEnvironmentResume   = "environment_resume"
	TypeEnvironmentResumed  = "environment_resumed"
)

type EnvironmentSuspendPayload struct {
	// Rollback permits a no-op resume on the still-running source after an
	// uncertain quiesce. It never authorizes cold replacement on snapshot restore.
	Rollback      bool   `json:"rollback,omitempty"`
	EnvironmentID string `json:"environment_id"`
	SuspendID     string `json:"suspend_id"`
}

type EnvironmentSuspendResultPayload struct {
	EnvironmentID string `json:"environment_id"`
	SuspendID     string `json:"suspend_id"`
	Accepted      bool   `json:"accepted"`
	ErrorCode     string `json:"error_code,omitempty"`
}

// SameSuspension compares ownership, independently of the requested recovery mode.
func (p EnvironmentSuspendPayload) SameSuspension(other EnvironmentSuspendPayload) bool {
	return p.EnvironmentID == other.EnvironmentID && p.SuspendID == other.SuspendID
}
