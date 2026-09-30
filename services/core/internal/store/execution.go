package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
)

// ErrExecutionAuthority rejects an execution-only operation on a pooled Store.
var ErrExecutionAuthority = errors.New("operation requires the execution writer")

// transactor runs one transaction: pgunit's Pool or its execution Lease.
type transactor interface {
	Transaction(context.Context, func(context.Context, pgx.Tx) error) error
}

// NewExecution takes the database's execution lease on a dedicated connection
// and returns the execution writer built on it. The writer's Session and
// execution-only transactions run on the leased connection; reads keep the pool.
// Keep public admission on the pooled Store. Losing or closing the lease never
// falls back to a pooled writer. It fails when another service owns the database.
func NewExecution(ctx context.Context, s *Store) (*Store, error) {
	lease, err := pgunit.AcquireLease(ctx, s.pool)
	if err != nil {
		return nil, err
	}
	writer := *s
	writer.writer, writer.lease = lease, lease
	return &writer, nil
}

// checkExecutionAuthority only validates. The connection was fixed when the
// Store was constructed.
func (s *Store) checkExecutionAuthority() error {
	if s.lease == nil {
		return ErrExecutionAuthority
	}
	return nil
}

// CheckExecutionOwnership validates the current writer before external preparation.
func (s *Store) CheckExecutionOwnership(ctx context.Context) error {
	if err := s.checkExecutionAuthority(); err != nil {
		return err
	}
	return s.lease.CheckOwnership(ctx)
}

// CancelExecutionOperations cancels coordinator-owned contexts between leased
// operations; see pgunit.Lease.CancelOperations for the constraints on cancel.
func (s *Store) CancelExecutionOperations(ctx context.Context, cancel context.CancelFunc) error {
	if err := s.checkExecutionAuthority(); err != nil {
		return err
	}
	return s.lease.CancelOperations(ctx, cancel)
}

// CloseExecution releases the execution lease and waits for connection cleanup
// within ctx. A later call resumes that wait. The writer cannot write afterwards.
func (s *Store) CloseExecution(ctx context.Context) error {
	if err := s.checkExecutionAuthority(); err != nil {
		return err
	}
	return s.lease.Close(ctx)
}
