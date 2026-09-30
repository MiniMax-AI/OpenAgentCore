package store

import (
	"context"
	"errors"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// BeginTurnArtifactCapture separates native completion from bounded output publication.
// Later messages use the existing reservation path instead of the finished executor.
func (s *Store) BeginTurnArtifactCapture(ctx context.Context, tenantID, sessionID, turnID string, appliedThrough int64) error {
	lookup, err := turnLookup(tenantID, sessionID, turnID)
	if err != nil {
		return err
	}
	if appliedThrough < 0 {
		return sessions.ErrInvalidInput
	}
	return s.withSession(ctx, tenantID, sessionID, func(ctx context.Context, q *sqlc.Queries, session pgtype.UUID) error {
		turn, err := q.GetTurn(ctx, lookup)
		if errors.Is(err, pgx.ErrNoRows) {
			return sessions.ErrNotFound
		}
		if err != nil {
			return err
		}
		if turn.Status != sessions.TurnInProgress || turn.CancelRequestedAt.Valid {
			return sessions.ErrTurnConflict
		}
		pending, err := q.HasUnappliedMessages(ctx, sqlc.HasUnappliedMessagesParams{SessionID: session, TurnID: lookup.ID, Sequence: appliedThrough})
		if err != nil {
			return err
		}
		if pending {
			return sessions.ErrUnappliedInputs
		}
		count, err := q.BeginTurnArtifactCapture(ctx, sqlc.BeginTurnArtifactCaptureParams{SessionID: session, ID: lookup.ID})
		if err == nil && count != 1 {
			return sessions.ErrTurnConflict
		}
		return err
	})
}
