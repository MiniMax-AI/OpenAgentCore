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
	return s.withLockedSession(ctx, tenantID, sessionID, true, func(ctx context.Context, q *sqlc.Queries, session sqlc.LockSessionRow) error {
		audit := func() error {
			return auditpg.RecordWriteAudit(ctx, q, tenantID, "delete", "session", uuid.UUID(session.ID.Bytes).String(), "")
		}
		if session.DeletedAt.Valid {
			return audit()
		}
		if err := requireSessionSettled(ctx, q, session.ID); err != nil {
			return err
		}
		if err := q.DeleteSessionArtifacts(ctx, session.ID); err != nil {
			return err
		}
		if err := q.ReleaseUnallocatedRuntimePlacement(ctx, session.ID); err != nil {
			return err
		}
		if err := q.MarkSessionDeleted(ctx, session.ID); err != nil {
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
	_, pending, err := environmentInputState(ctx, q, session)
	if err != nil {
		return err
	}
	if pending {
		return sessions.ErrNotIdle
	}
	return nil
}

// cancelSessionWork requests cancellation of active work for Runtime cleanup.
func cancelSessionWork(ctx context.Context, q *sqlc.Queries, session pgtype.UUID) error {
	turn, err := q.GetActiveTurn(ctx, session)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if err == nil {
		if err := requestTurnCancel(ctx, q, session, turn); err != nil {
			return err
		}
	}
	if err := q.CancelSessionEnvironmentInput(ctx, session); err != nil {
		return err
	}
	return nil
}

func requestTurnCancel(ctx context.Context, q *sqlc.Queries, session pgtype.UUID, turn sqlc.Turn) error {
	if err := q.RequestTurnCancel(ctx, sqlc.RequestTurnCancelParams{ID: turn.ID, SessionID: session}); err != nil {
		return err
	}
	if turn.Status == sessions.TurnQueued {
		cancelled, err := q.SessionEventTurn(ctx, sqlc.SessionEventTurnParams{SessionID: session, ID: turn.ID})
		if err != nil {
			return err
		}
		ending, err := sessionpg.LoadEnding(ctx, q, session, cancelled.ID)
		if err != nil {
			return err
		}
		if err := sessionpg.ApplyTurnEnd(ctx, q, session, cancelled.ID, sessions.EndTurn(turnFromRow(cancelled), ending)); err != nil {
			return err
		}
	} else if turn.Status == sessions.TurnWaiting && !turn.CancelRequestedAt.Valid {
		cancelling, err := q.SessionEventTurn(ctx, sqlc.SessionEventTurnParams{SessionID: session, ID: turn.ID})
		if err != nil {
			return err
		}
		usage, err := sessionpg.LoadUsage(ctx, q, session)
		if err != nil {
			return err
		}
		if err := sessionpg.AppendChanges(ctx, q, session, sessions.ActivityChange(turnFromRow(cancelling), usage, nil)); err != nil {
			return err
		}
	}
	return nil
}
