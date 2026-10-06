package sessions

import (
	"context"
	"encoding/json"
	"time"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
)

// SessionReader reads Sessions, their public change journal and their
// diagnostics.
type SessionReader interface {
	// GetSession returns the tenant's visible Session with its Environment,
	// input activity and latest Turn, read from one snapshot, or ErrNotFound.
	GetSession(ctx context.Context, tenantID, sessionID string) (Session, error)
	// ListSessions returns one page of the tenant's visible Sessions in
	// creation-time and ID order; a non-nil agentID keeps that root Agent's
	// Sessions. The cursor is the last returned Session ID and must belong to
	// the tenant; it grants no additional access. A limit outside 1..100 is
	// ErrInvalidInput.
	ListSessions(ctx context.Context, tenantID, cursor string, limit int, ascending bool, agentID *string) (Page, error)
	// SessionStreamSnapshot reads the projection GetSession returns and the
	// committed Session event cursor from the same snapshot.
	SessionStreamSnapshot(ctx context.Context, tenantID, sessionID string) (Session, int64, error)
	// SessionEventCursor returns the sequence of the visible Session's latest
	// public change, or ErrNotFound.
	SessionEventCursor(ctx context.Context, tenantID, sessionID string) (int64, error)
	// ListSessionEvents returns the retained public changes after the
	// sequence, in order. A negative after is ErrInvalidInput; a pruned change
	// in between is ErrStreamGap.
	ListSessionEvents(ctx context.Context, tenantID, sessionID string, after int64) ([]SessionChange, error)
	// GetTurnDiagnosticsSnapshot reads a root Turn and the timing of up to
	// 1000 of its root Items with the Session in one read-only snapshot.
	GetTurnDiagnosticsSnapshot(ctx context.Context, tenantID, sessionID, turnID string) (TurnDiagnosticsSnapshot, error)
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
	// GetManagedSessionArchive reads the resource disposal of the tenant's
	// hosted Session and never contacts compute. A missing Session is
	// ErrNotFound; one that is not hosted is ErrInvalidInput.
	GetManagedSessionArchive(ctx context.Context, tenantID, sessionID string) (ManagedArchive, error)
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
	SessionID     string `json:"session_id"`
	EnvironmentID string `json:"environment_id"`
	State         string `json:"state"`
}
