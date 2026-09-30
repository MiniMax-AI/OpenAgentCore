package store

import (
	"context"
	"errors"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/auditpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/sessionpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// DeleteSession removes public access to a durably idle or failed Session while
// retaining state needed to settle execution. The decision is taken under the
// Session lock that also orders Turn and input admission, so a concurrent
// admission either commits first and is rejected here, or observes the deletion.
// The owner's repeated deletion succeeds without another resource write; foreign and
// missing Sessions remain not found.
func (s *Store) DeleteSession(ctx context.Context, tenantID, sessionID string) error {
	return s.withLockedSession(ctx, tenantID, sessionID, true, func(ctx context.Context, q *sqlc.Queries, session pgtype.UUID, locked sessions.LockedSession) error {
		audit := func() error {
			return auditpg.RecordWriteAudit(ctx, q, tenantID, "delete", "session", uuid.UUID(session.Bytes).String(), "")
		}
		if locked.Deleted {
			return audit()
		}
		if err := requireSessionSettled(ctx, q, session); err != nil {
			return err
		}
		if err := q.DeleteSessionArtifacts(ctx, session); err != nil {
			return err
		}
		if err := q.ReleaseUnallocatedRuntimePlacement(ctx, session); err != nil {
			return err
		}
		if err := q.MarkSessionDeleted(ctx, session); err != nil {
			return err
		}
		return audit()
	})
}

// requireSessionSettled rejects a queued, in-progress or waiting Turn, which
// includes pending required actions and function results, and a pending input
// reservation: queued later input, self-hosted input awaiting a connection, or
// hosted initial input while provisioning. Terminal idle and failed Sessions pass.
func requireSessionSettled(ctx context.Context, q *sqlc.Queries, session pgtype.UUID) error {
	if _, err := q.GetActiveTurn(ctx, session); err == nil {
		return sessions.ErrNotIdle
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	state, err := sessionpg.LoadEnvironmentInput(ctx, q, session)
	if err != nil {
		return err
	}
	if _, pending := sessions.InputActivity(state); pending {
		return sessions.ErrNotIdle
	}
	return nil
}
