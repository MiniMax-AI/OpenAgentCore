package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/jsonobject"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/sessionpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// CompleteExecution commits the outcome and native continuity under the admission lock.
func (s *Store) CompleteExecution(ctx context.Context, tenantID, sessionID, turnID, status string, outcome json.RawMessage, nativeID string, appliedThrough int64) (sessions.Turn, error) {
	p, err := sessionpg.TurnLookup(tenantID, sessionID, turnID)
	if err != nil {
		return sessions.Turn{}, err
	}
	if !sessions.TerminalStatus(status) || len(outcome) > 512*1024 || len(nativeID) > 512 || appliedThrough < 0 {
		return sessions.Turn{}, sessions.ErrInvalidInput
	}
	outcome, err = jsonobject.Normalize(outcome)
	if err != nil {
		return sessions.Turn{}, fmt.Errorf("%w: %w", sessions.ErrInvalidInput, err)
	}
	var row sqlc.Turn
	err = s.withSession(ctx, tenantID, sessionID, func(ctx context.Context, q *sqlc.Queries, session pgtype.UUID) error {
		current, err := q.GetTurn(ctx, p)
		if errors.Is(err, pgx.ErrNoRows) {
			return sessions.ErrNotFound
		} else if err != nil {
			return err
		}
		if current.Status != sessions.TurnInProgress && (current.Status != sessions.TurnWaiting || status == sessions.TurnCompleted) {
			return sessions.ErrTurnConflict
		}
		if status == sessions.TurnCompleted {
			pending, err := q.HasUnappliedMessages(ctx, sqlc.HasUnappliedMessagesParams{SessionID: session, TurnID: p.ID, Sequence: appliedThrough})
			if err != nil {
				return err
			}
			if pending {
				return sessions.ErrUnappliedInputs
			}
		}
		sourceCompleted := pgtype.Timestamptz{}
		if status == sessions.TurnCompleted {
			var snapshot struct {
				Done *struct {
					SourceCompletedAtMS *int64 `json:"source_completed_at_ms"`
				} `json:"done"`
			}
			if json.Unmarshal(outcome, &snapshot) != nil {
				return sessions.ErrInvalidInput
			}
			if snapshot.Done != nil && snapshot.Done.SourceCompletedAtMS != nil {
				ms := *snapshot.Done.SourceCompletedAtMS
				if ms <= 0 {
					return sessions.ErrInvalidInput
				}
				// Native and Core timestamps come from independent host clocks.
				// Preserve source time; committed activity uses the database clock.
				sourceCompleted = pgtype.Timestamptz{Time: time.UnixMilli(ms), Valid: true}
			}
		}
		row, err = q.TransitionTurn(ctx, sqlc.TransitionTurnParams{ID: p.ID, SessionID: session, ExpectedStatus: current.Status, NewStatus: status, Outcome: outcome, SourceCompletedAt: sourceCompleted})
		if errors.Is(err, pgx.ErrNoRows) {
			return sessions.ErrTurnConflict
		}
		if err != nil {
			return err
		}
		if err = sessions.AppendTurnEvent(ctx, sessionpg.BindSession(q, p.TenantID, session), uuid.UUID(row.ID.Bytes).String(), row.EventCount, sessions.ExecutionEvent{Kind: "execution_" + status, Payload: outcome}); err != nil {
			return err
		}
		if nativeID != "" {
			n, err := q.RememberNativeSession(ctx, sqlc.RememberNativeSessionParams{SessionID: session, NativeSessionID: nativeID})
			if err != nil {
				return err
			}
			if n != 1 {
				return sessions.ErrNotFound
			}
		}
		row, err = q.GetTurn(ctx, p)
		if err != nil {
			return err
		}
		ending, err := sessionpg.LoadEnding(ctx, q, session, row.ID)
		if err != nil {
			return err
		}
		return sessionpg.ApplyTurnEnd(ctx, q, session, row.ID, sessions.EndTurn(sessionpg.TurnFromRow(row), ending))
	})
	if err != nil {
		return sessions.Turn{}, err
	}
	return sessionpg.TurnFromRow(row), nil
}
