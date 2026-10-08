package sessions

import (
	"context"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
)

// ModelExecutionReader reads the model execution a Session froze at creation.
type ModelExecutionReader interface {
	// SessionModelExecution opens the model provider the tenant's Session
	// froze at creation. A Session without one is ErrNotFound; a frozen
	// provider that does not open, decode or validate is an internal error.
	SessionModelExecution(ctx context.Context, tenant, session string) (*v1.ModelProviderInput, error)
}
