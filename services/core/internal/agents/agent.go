package agents

import (
	"encoding/json"
	"fmt"
	"time"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
)

// Agent is saved configuration owned by a tenant. It has no Harness binding or
// live execution state; Session snapshots are separate objects.
type Agent struct {
	ID            string
	TenantID      string
	Metadata      map[string]string
	Configuration json.RawMessage
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// MaxConfigurationBytes bounds a saved configuration and an update's patch.
const MaxConfigurationBytes = 512 * 1024

// MaxPageSize bounds one page of ListAgents.
const MaxPageSize = 100

// ListQuery selects one page of a tenant's Agents in creation order. After is
// the last Agent of the previous page; an After that names no Agent of the
// tenant is ErrNotFound.
type ListQuery struct {
	TenantID  string
	After     string
	Limit     int
	Ascending bool
}

// Validate checks the page size.
func (q ListQuery) Validate() error {
	if q.Limit < 1 || q.Limit > MaxPageSize {
		return fmt.Errorf("%w: page size must be 1..%d", ErrInvalidInput, MaxPageSize)
	}
	return nil
}

// Page is one page of Agents. NextCursor is empty on the last page.
type Page struct {
	Agents     []Agent
	NextCursor string
}

// ModelProviderChange replaces an Agent's saved model provider bundle. A nil
// Provider removes it.
type ModelProviderChange struct {
	Provider *v1.ModelProviderInput
}
