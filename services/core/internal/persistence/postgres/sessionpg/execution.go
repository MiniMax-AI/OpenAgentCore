package sessionpg

import (
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

// Execution is the Session storage of the execution owner. Every transaction
// runs on the connection that holds the execution lease, which the owner
// acquired and closes; losing or closing the lease fails the operation and
// never falls back to the pool.
type Execution struct{ lease *pgunit.Lease }

// NewExecution builds the Session execution storage on a lease it borrows.
func NewExecution(lease *pgunit.Lease) *Execution { return &Execution{lease: lease} }

var _ sessions.ExecutionStorage = (*Execution)(nil)
