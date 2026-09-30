package store

import (
	"context"
	"errors"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
)

// ErrExecutionAuthority rejects an execution-only operation on a pooled Store.
var ErrExecutionAuthority = errors.New("operation requires the execution writer")

// NewExecution returns the execution writer built on lease, which the caller
// acquired and closes. The writer's Session and execution-only transactions run
// on the leased connection; reads keep the pool. Keep public admission on the
// pooled Store. Losing or closing the lease never falls back to a pooled writer.
func NewExecution(s *Store, lease *pgunit.Lease) *Store {
	writer := *s
	writer.writer, writer.lease = lease, lease
	return &writer
}

// checkExecutionAuthority only validates. The connection was fixed when the
// Store was constructed.
func (s *Store) checkExecutionAuthority() error {
	if s.lease == nil {
		return ErrExecutionAuthority
	}
	return nil
}

// checkExecutionOwnership confirms, before an execution-only pooled read, that
// the borrowed lease still owns the database.
func (s *Store) checkExecutionOwnership(ctx context.Context) error {
	if err := s.checkExecutionAuthority(); err != nil {
		return err
	}
	return s.lease.CheckOwnership(ctx)
}
