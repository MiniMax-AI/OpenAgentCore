package agents

import (
	"context"
	"encoding/json"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
)

// Storage persists Agent writes. Each method runs in one transaction that also
// records the write audit. An agent ID that cannot name an Agent is a missing
// Agent.
type Storage interface {
	// CreateAgent assigns the Agent's ID and saves it with its sealed model
	// provider bundle.
	CreateAgent(context.Context, NewAgent) (Agent, error)
	// WithAgentUpdate locks the tenant's Agent and runs update; the revision
	// it applies commits only when update returns nil.
	WithAgentUpdate(ctx context.Context, tenantID, agentID string, update func(UpdateTx) error) error
	// DeleteAgent removes the Agent and its model provider bundle and returns
	// the deleted ID.
	DeleteAgent(ctx context.Context, tenantID, agentID string) (string, error)
}

// UpdateTx is one locked Agent inside WithAgentUpdate.
type UpdateTx interface {
	// LoadAgent returns the locked Agent, or ErrNotFound.
	LoadAgent() (Agent, error)
	// ApplyRevision stores the revision and returns the updated Agent.
	ApplyRevision(Revision) (Agent, error)
}

// NewAgent is a validated Agent to create.
type NewAgent struct {
	TenantID string
	// Metadata is the encoded metadata object.
	Metadata      json.RawMessage
	Configuration json.RawMessage
	// ModelProvider is sealed beside the Agent; nil saves none.
	ModelProvider *v1.ModelProviderInput
}

// Revision is the complete next state of an Agent.
type Revision struct {
	// Metadata is the encoded metadata object.
	Metadata      json.RawMessage
	Configuration json.RawMessage
	// ModelProvider nil keeps the saved bundle.
	ModelProvider *ModelProviderChange
}
