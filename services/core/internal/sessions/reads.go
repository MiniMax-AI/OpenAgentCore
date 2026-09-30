package sessions

import (
	"time"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
)

type ItemPage struct {
	Items   []v1.Item
	HasMore bool
}

type Artifact struct {
	ID            string
	SessionID     string
	TurnID        string
	EnvironmentID string
	Path          string
	SizeBytes     int64
	CreatedAt     time.Time
}

type ArtifactPage struct {
	Artifacts  []Artifact
	NextCursor string
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
