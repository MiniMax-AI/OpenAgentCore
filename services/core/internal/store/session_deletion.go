package store

import (
	"context"
	"errors"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// ErrSessionNotIdle rejects deletion of a Session that still has work or input
// pending. Callers cancel first and delete after the Session settles.
var ErrSessionNotIdle = errors.New("session must be durably idle or failed without required actions before deletion")

// DeleteSession removes public access to a durably idle or failed Session while
// retaining state needed to settle execution. The decision is taken under the
// Session lock that also orders Turn and input admission, so a concurrent
// admission either commits first and is rejected here, or observes the deletion.
// The owner's repeated deletion succeeds without another resource write; foreign and
// missing Sessions remain not found.
func (s *Store) DeleteSession(ctx context.Context, tenantID, sessionID string) error {
	return s.withLockedSession(ctx, tenantID, sessionID, true, func(ctx context.Context, q *sqlc.Queries, session sqlc.LockSessionRow) error {
		audit := func() error {
			return recordWriteAudit(ctx, q, tenantID, "delete", "session", uuid.UUID(session.ID.Bytes).String(), "")
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
		return ErrSessionNotIdle
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	_, pending, err := environmentInputState(ctx, q, session)
	if err != nil {
		return err
	}
	if pending {
		return ErrSessionNotIdle
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
	if turn.Status == TurnQueued {
		cancelled, err := q.SessionEventTurn(ctx, sqlc.SessionEventTurnParams{SessionID: session, ID: turn.ID})
		if err != nil {
			return err
		}
		if err := recordTurnChange(ctx, q, cancelled, false); err != nil {
			return err
		}
	} else if turn.Status == TurnWaiting && !turn.CancelRequestedAt.Valid {
		cancelling, err := q.SessionEventTurn(ctx, sqlc.SessionEventTurnParams{SessionID: session, ID: turn.ID})
		if err != nil {
			return err
		}
		if err := recordSessionActivity(ctx, q, cancelling, nil); err != nil {
			return err
		}
	}
	return nil
}
