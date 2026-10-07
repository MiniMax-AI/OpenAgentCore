package proto

import (
	"errors"

	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxbootstrap"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxlink"
)

// Assignment traffic binds a Session to the Runtime that runs it. Envelope.ID
// correlates a request with its assignment_status, and Envelope.Assignment
// names the assignment on both.
const (
	// TypeAssignmentBind binds the Session to this Runtime. The Runtime
	// replies bound before Core sends the Session's first operation.
	TypeAssignmentBind = "assignment_bind"
	// TypeAssignmentRelease ends the assignment. The Runtime stops the
	// Session's work and closes its Executors before it replies.
	TypeAssignmentRelease = "assignment_release"
	TypeAssignmentStatus  = "assignment_status"
	// TypeProtocolError answers a request the Runtime cannot route. It
	// carries the request's ID and type.
	TypeProtocolError = "protocol_error"
)

// Assignment states an assignment_status reports.
const (
	AssignmentBound       = "bound"
	AssignmentReleased    = "released"
	AssignmentHomeRemoved = "home_removed"
	AssignmentFailed      = "failed"
)

// Error codes of assignment fencing. A frame whose reference is older than
// the Runtime's assignment, or names a released one, is stale; a frame that
// names no assignment the Runtime holds, or another Session or Environment,
// conflicts.
const (
	AssignmentStale    = "assignment_stale"
	AssignmentConflict = "assignment_conflict"
	// UnsupportedOperation rejects an operation the Runtime does not declare,
	// before any effect.
	UnsupportedOperation = "unsupported_operation"
	// CleanupUnconfirmed reports a release whose cleanup did not finish; the
	// assignment stays released and a retry repeats the cleanup.
	CleanupUnconfirmed = "cleanup_unconfirmed"
)

// AssignmentRef names one assignment of a Session to a Runtime. Core advances
// Epoch whenever it changes the assignment's desired state.
type AssignmentRef struct {
	SessionID    string `json:"session_id"`
	AssignmentID string `json:"assignment_id"`
	Epoch        uint64 `json:"epoch"`
}

// Valid reports whether the reference names an assignment.
func (r AssignmentRef) Valid() bool {
	return r.SessionID != "" && len(r.SessionID) <= 128 && r.AssignmentID != "" && len(r.AssignmentID) <= 128 && r.Epoch > 0
}

// AssignmentBindPayload is what the Runtime fences the Session's frames with.
// EnvironmentID is empty for a Session without an Environment. Resource and
// AttachGrant come together, only to an agent host whose Environment has a
// Link resource: the agent host attaches to Resource with AttachGrant, which
// is secret.
type AssignmentBindPayload struct {
	EnvironmentID string                     `json:"environment_id,omitempty"`
	Resource      *sandboxbootstrap.Resource `json:"resource,omitempty"`
	AttachGrant   []byte                     `json:"attach_grant,omitempty"`
}

// Validate checks that Resource and AttachGrant come together and that
// Resource is a resource of the Environment.
func (p AssignmentBindPayload) Validate() error {
	if p.Resource == nil && len(p.AttachGrant) == 0 {
		return nil
	}
	if p.Resource == nil || len(p.AttachGrant) == 0 || len(p.AttachGrant) > sandboxlink.MaxGrantBytes ||
		p.Resource.Validate() != nil || p.Resource.EnvironmentID != p.EnvironmentID {
		return errors.New("assignment_bind requires a valid resource of its Environment with an attach grant")
	}
	return nil
}

// AssignmentReleasePayload asks the Runtime to remove the Session's native
// home after its work stops. Only a Runtime that declares home removal accepts
// RemoveHome.
type AssignmentReleasePayload struct {
	RemoveHome bool `json:"remove_home"`
}

type AssignmentStatusPayload struct {
	State     string `json:"state"`
	ErrorCode string `json:"error_code,omitempty"`
}

type ProtocolErrorPayload struct {
	Type      string `json:"type"`
	ErrorCode string `json:"error_code"`
}
