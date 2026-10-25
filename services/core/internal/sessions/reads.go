package sessions

import (
	"context"
	"encoding/json"
	"time"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
)

type ManagedArchiveState string

const (
	ManagedArchiveActive         ManagedArchiveState = "active"
	ManagedArchiveCleanupPending ManagedArchiveState = "cleanup_pending"
	ManagedArchiveReleased       ManagedArchiveState = "released"
)

// SessionReader reads Sessions, their public change journal and their
// diagnostics.
type SessionReader interface {
	// GetSession returns the tenant's visible Session with its Environment,
	// input activity and latest Turn, read from one snapshot, or ErrNotFound.
	GetSession(ctx context.Context, tenantID, sessionID string) (Session, error)
	// GetSessionExecutionConfiguration reads only the safe committed
	// configuration. It never loads provider ciphertext, current defaults or
	// runtime health.
	GetSessionExecutionConfiguration(ctx context.Context, tenantID, sessionID string) (v1.SessionExecutionConfiguration, error)
	// MeasuredSessionUsage returns Core-internal measured usage for Runtime
	// telemetry: the sum of every recorded root Turn snapshot, active Turns
	// included, and null only when nothing is recorded. Public Session usage
	// keeps the official rule. A missing Session reads as null, so callers
	// resolve the Session first.
	MeasuredSessionUsage(ctx context.Context, tenantID, sessionID string) (json.RawMessage, error)
}

// ItemDiagnosticTiming records Core database receipt and settlement, never native
// execution duration. Historical terminal Items can have unknown settlement.
type ItemDiagnosticTiming struct {
	ItemID      string
	StartedAt   time.Time
	CompletedAt *time.Time
}

type TurnDiagnosticsSnapshot struct {
	Session        Session
	Turn           Turn
	Items          []ItemDiagnosticTiming
	ItemsTruncated bool
}

// ManagedArchive reports resource disposal, not archive request provenance
// or Turn settlement. Existing expiry and failed provisioning use the same states.
type ManagedArchive struct {
	SessionID     string              `json:"session_id" binding:"required"`
	EnvironmentID string              `json:"environment_id" binding:"required"`
	State         ManagedArchiveState `json:"state" binding:"required"`
}
