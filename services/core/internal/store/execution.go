package store

import "github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"

// NewExecution returns the execution writer built on lease, which the caller
// acquired and closes. The writer's Session and execution-only transactions run
// on the leased connection; reads keep the pool. Keep public admission on the
// pooled Store. Losing or closing the lease never falls back to a pooled writer.
func NewExecution(s *Store, lease *pgunit.Lease) *Store {
	writer := *s
	writer.writer, writer.lease = lease, lease
	return &writer
}
