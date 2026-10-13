// Package workspaces owns durable Environment filesystem identities. The
// workspacefs protocol owns adapter configuration and attachment validation.
package workspaces

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/workspacefs"
)

var (
	ErrNotFound      = errors.New("workspace not found")
	ErrConflict      = errors.New("workspace ownership or lifecycle conflict")
	ErrNotConfigured = errors.New("workspace filesystem is not configured")
	// ErrCapacityExhausted refuses a new workspace whose admission would exceed
	// the tenant capacity policy. It is an unavailable outcome, not invalid input.
	ErrCapacityExhausted = fmt.Errorf("%w: tenant workspace capacity exhausted", workspacefs.ErrUnavailable)
)

type State string

const (
	Creating State = "creating"
	Ready    State = "ready"
	Deleting State = "deleting"
	Deleted  State = "deleted"
)

// Record retains its reference, immutable configuration and admitted capacity
// after deletion so late creation receipts cannot reintroduce a deleted
// workspace. Configuration carries the decrypted credential for Control only.
type Record struct {
	Reference     workspacefs.Reference
	Configuration workspacefs.ConfigurationRecord
	CapacityMiB   uint32
	State         State
	Attachment    *workspacefs.Attachment
}

// Selection is the workspace storage runtime setting: the selected immutable
// configuration and the tenant capacity policy, written together. Zero
// TenantCapacityMiB disables tenant admission; a positive value requires a
// configuration that enforces capacity quotas.
type Selection struct {
	workspacefs.Configuration
	TenantCapacityMiB uint32 `json:"tenant_capacity_mib" binding:"required"`
}

// SelectionInput adds the write-only adapter credential. Omitting it when
// re-selecting an existing configuration retains its stored credential.
type SelectionInput struct {
	Selection
	Credential json.RawMessage
}
