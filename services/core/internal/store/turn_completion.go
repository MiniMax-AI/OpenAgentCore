package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

var ErrUnappliedInputs = errors.New("turn has messages without an executor receipt")

// CompleteExecution commits the outcome and native continuity under the admission lock.
func (s *Store) CompleteExecution(ctx context.Context, tenantID, sessionID, turnID, status string, outcome json.RawMessage, nativeID string, appliedThrough int64) (Turn, error) {
	p, err := turnLookup(tenantID, sessionID, turnID)
	if err != nil {
		return Turn{}, err
	}
	if !terminalStatus(status) || len(outcome) > 512*1024 || len(nativeID) > 512 || appliedThrough < 0 {
		return Turn{}, ErrInvalidInput
	}
	outcome, err = canonicalJSONObject(outcome)
	if err != nil {
		return Turn{}, err
	}
	var row sqlc.Turn
	err = s.withSession(ctx, tenantID, sessionID, func(ctx context.Context, q *sqlc.Queries, session pgtype.UUID) error {
		current, err := q.GetTurn(ctx, p)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		if current.Status != TurnInProgress && (current.Status != TurnWaiting || status == TurnCompleted) {
			return ErrTurnConflict
		}
		if status == TurnCompleted {
			pending, err := q.HasUnappliedMessages(ctx, sqlc.HasUnappliedMessagesParams{SessionID: session, TurnID: p.ID, Sequence: appliedThrough})
			if err != nil {
				return err
			}
			if pending {
				return ErrUnappliedInputs
			}
		}
		sourceCompleted := pgtype.Timestamptz{}
		if status == TurnCompleted {
			var snapshot struct {
				Done *struct {
					SourceCompletedAtMS *int64 `json:"source_completed_at_ms"`
				} `json:"done"`
			}
			if json.Unmarshal(outcome, &snapshot) != nil {
				return ErrInvalidInput
			}
			if snapshot.Done != nil && snapshot.Done.SourceCompletedAtMS != nil {
				ms := *snapshot.Done.SourceCompletedAtMS
				if ms <= 0 {
					return ErrInvalidInput
				}
				// Native and Core timestamps come from independent host clocks.
				// Preserve source time; committed activity uses the database clock.
				sourceCompleted = pgtype.Timestamptz{Time: time.UnixMilli(ms), Valid: true}
			}
		}
		row, err = q.TransitionTurn(ctx, sqlc.TransitionTurnParams{ID: p.ID, SessionID: session, ExpectedStatus: current.Status, NewStatus: status, Outcome: outcome, SourceCompletedAt: sourceCompleted})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrTurnConflict
		}
		if err != nil {
			return err
		}
		if err = insertTurnEvent(ctx, q, row, "execution_"+status, outcome); err != nil {
			return err
		}
		if nativeID != "" {
			n, err := q.RememberNativeSession(ctx, sqlc.RememberNativeSessionParams{SessionID: session, NativeSessionID: nativeID})
			if err != nil {
				return err
			}
			if n != 1 {
				return ErrNotFound
			}
		}
		row, err = q.GetTurn(ctx, p)
		if err != nil {
			return err
		}
		return recordTurnChange(ctx, q, row, false)
	})
	if err != nil {
		return Turn{}, err
	}
	return turnFromRow(row), nil
}
