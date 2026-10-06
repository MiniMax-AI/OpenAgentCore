package sessionpg

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

// WithTurns runs apply in a Session transaction on the lease, including for a
// publicly deleted Session.
func (e *Execution) WithTurns(ctx context.Context, tenantID, sessionID string, apply func(context.Context, sessions.TurnTx) error) error {
	tenant, err := parseID(tenantID)
	if err != nil {
		return err
	}
	session, err := parseID(sessionID)
	if err != nil {
		return err
	}
	return WithSession(ctx, e.lease, tenant, session, func(ctx context.Context, q *sqlc.Queries, _ sessions.LockedSession) error {
		return apply(ctx, BindSession(q, tenant, session))
	})
}

func (t *SessionTx) ApplyTurnStatus(ctx context.Context, turn string, change sessions.TurnStatusChange) (sessions.Turn, error) {
	id, err := parseID(turn)
	if err != nil {
		return sessions.Turn{}, err
	}
	row, err := t.q.TransitionTurn(ctx, sqlc.TransitionTurnParams{
		ID: id, SessionID: t.session, ExpectedStatus: change.Expected, NewStatus: change.Status, Outcome: change.Outcome,
		SourceCompletedAt: pgtype.Timestamptz{Time: change.SourceCompletedAt, Valid: !change.SourceCompletedAt.IsZero()},
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return sessions.Turn{}, sessions.ErrTurnConflict
	}
	if err != nil {
		return sessions.Turn{}, err
	}
	return TurnFromRow(row), nil
}

func (t *SessionTx) HasUnappliedInputs(ctx context.Context, turn string, appliedThrough int64) (bool, error) {
	id, err := parseID(turn)
	if err != nil {
		return false, err
	}
	return t.q.HasUnappliedMessages(ctx, sqlc.HasUnappliedMessagesParams{SessionID: t.session, TurnID: id, Sequence: appliedThrough})
}

func (t *SessionTx) RememberNativeSession(ctx context.Context, native string) error {
	n, err := t.q.RememberNativeSession(ctx, sqlc.RememberNativeSessionParams{SessionID: t.session, NativeSessionID: native})
	if err != nil {
		return err
	}
	if n != 1 {
		return sessions.ErrNotFound
	}
	return nil
}

func (t *SessionTx) BeginArtifactCapture(ctx context.Context, turn string) error {
	id, err := parseID(turn)
	if err != nil {
		return err
	}
	n, err := t.q.BeginTurnArtifactCapture(ctx, sqlc.BeginTurnArtifactCaptureParams{SessionID: t.session, ID: id})
	if err != nil {
		return err
	}
	if n != 1 {
		return sessions.ErrTurnConflict
	}
	return nil
}
