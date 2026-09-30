package modelconfiguration

import (
	"encoding/json"
	"time"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/google/uuid"
)

// Configuration is the safe view of one Harness's deployment default. The
// provider key is write-only; Provider reports only that one is configured.
// LastUsedAt, LastErrorCode and LastErrorAt are best-effort observations of
// committed root Turns that used this exact revision.
type Configuration struct {
	Harness       string
	Provider      v1.ModelProviderView
	Model         string
	HarnessConfig json.RawMessage
	UpdatedAt     time.Time
	LastUsedAt    *time.Time
	LastErrorCode *string
	LastErrorAt   *time.Time
}

// Replacement is one complete deployment default for a Harness, including its
// write-only provider key.
type Replacement struct {
	Harness       string
	Configuration v1.ModelConfigurationInput
}

// Snapshot is a decrypted deployment default for Session creation, paired with
// the private revision read with it. It never enters a public projection.
type Snapshot struct {
	Provider      *v1.ModelProviderInput
	Model         string
	HarnessConfig json.RawMessage
	Revision      uuid.UUID
}

// Record is a validated deployment default ready to store: its safe columns
// and the sealed complete bundle. Storage assigns a new revision to each
// Record it stores.
type Record struct {
	Harness       string
	Provider      v1.ModelProviderView
	Model         string
	HarnessConfig json.RawMessage
	Sealed        []byte
}

// Sealed is a stored bundle and the revision read with it.
type Sealed struct {
	Bundle   []byte
	Revision uuid.UUID
}
