package sessionpg

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

// LockSession locks the tenant's Session row, including a publicly deleted
// one, and returns what the lock shows. Every Session write takes this lock
// first, which orders input admission against completion and cancellation
// across service processes. A missing Session is sessions.ErrNotFound.
func LockSession(ctx context.Context, q *sqlc.Queries, tenant, session pgtype.UUID) (sessions.LockedSession, error) {
	row, err := q.LockSession(ctx, sqlc.LockSessionParams{TenantID: tenant, ID: session})
	if errors.Is(err, pgx.ErrNoRows) {
		return sessions.LockedSession{}, sessions.ErrNotFound
	}
	if err != nil {
		return sessions.LockedSession{}, err
	}
	return sessions.LockedSession{Deleted: row.DeletedAt.Valid}, nil
}

// WithSession runs one Session transaction on runner: it locks the tenant's
// Session, runs apply on the transaction's queries with what the lock shows,
// then prunes the Session's journal, and commits only when all of them
// succeed. apply applies the public view itself, with
// sessions.LockedSession.Public. It is the transaction runner of Session
// operations; other adapters lock the Session inside their own transaction
// with LockSession.
func WithSession(ctx context.Context, runner pgunit.Transactor, tenant, session pgtype.UUID, apply func(context.Context, *sqlc.Queries, sessions.LockedSession) error) error {
	return runner.Transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlc.New(tx)
		locked, err := LockSession(ctx, q, tenant, session)
		if err != nil {
			return err
		}
		if err := apply(ctx, q, locked); err != nil {
			return err
		}
		return PruneChanges(ctx, q, session)
	})
}
