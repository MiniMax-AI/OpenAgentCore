// Package workspaces owns durable Environment filesystem identities. The
// workspacefs protocol owns adapter configuration and attachment validation.
package workspaces

import (
	"errors"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/workspacefs"
)

var (
	ErrNotFound      = errors.New("workspace not found")
	ErrConflict      = errors.New("workspace ownership or lifecycle conflict")
	ErrNotConfigured = errors.New("workspace filesystem is not configured")
)

type State string

const (
	Creating State = "creating"
	Ready    State = "ready"
	Deleting State = "deleting"
	Deleted  State = "deleted"
)

// Record retains its reference and immutable configuration after deletion so
// late creation receipts cannot reintroduce a deleted workspace.
type Record struct {
	Reference     workspacefs.Reference
	Configuration workspacefs.Configuration
	State         State
	Attachment    *workspacefs.Attachment
}
