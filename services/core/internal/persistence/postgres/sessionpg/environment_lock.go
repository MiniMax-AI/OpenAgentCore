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

var errAttachmentMoved = errors.New("the Environment's attached Session changed while locking")

// WithEnvironment runs one Environment transaction on runner. It reads the
// attached Session, locks that Session if there is one, then locks the
// Environment and checks that the attachment did not move; it retries once
// when it did. The lock order is always Session, then Environment. apply gets
// what the locks show, with Deleted true for a Session-owned Environment whose
// Session was deleted and Engine the attached Session's, and the attached
// Session, which is invalid when none is attached. The attached Session's
// journal is pruned afterwards.
func WithEnvironment(ctx context.Context, runner pgunit.Transactor, tenant, environment pgtype.UUID, apply func(context.Context, *sqlc.Queries, sessions.LockedSession, pgtype.UUID) error) error {
	var err error
	for range 2 {
		err = runner.Transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
			return lockEnvironment(ctx, sqlc.New(tx), tenant, environment, apply)
		})
		if !errors.Is(err, errAttachmentMoved) {
			return err
		}
	}
	return sessions.ErrTurnConflict
}

func lockEnvironment(ctx context.Context, q *sqlc.Queries, tenant, environment pgtype.UUID, apply func(context.Context, *sqlc.Queries, sessions.LockedSession, pgtype.UUID) error) error {
	seen, err := q.GetEnvironmentAttachment(ctx, sqlc.GetEnvironmentAttachmentParams{TenantID: tenant, ID: environment})
	if errors.Is(err, pgx.ErrNoRows) {
		return sessions.ErrNotFound
	}
	if err != nil {
		return err
	}
	var locked sessions.LockedSession
	if seen.AttachedSessionID.Valid {
		if locked, err = LockSession(ctx, q, tenant, seen.AttachedSessionID); err != nil {
			return err
		}
	}
	row, err := q.LockEnvironment(ctx, sqlc.LockEnvironmentParams{TenantID: tenant, ID: environment})
	if err != nil {
		return err
	}
	if row.AttachedSessionID != seen.AttachedSessionID {
		return errAttachmentMoved
	}
	locked.Deleted = row.SessionOwned && !row.AttachedSessionID.Valid
	if err := apply(ctx, q, locked, row.AttachedSessionID); err != nil {
		return err
	}
	if !row.AttachedSessionID.Valid {
		return nil
	}
	return PruneChanges(ctx, q, row.AttachedSessionID)
}
