package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/jsonobject"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/sessionpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

// GetTurn reads a root Turn. A Subagent Turn ID is not found here, exactly like
// a missing one; GetSubagentTurn reads child Turns.
func (s *Store) GetTurn(ctx context.Context, tenantID, sessionID, turnID string) (sessions.Turn, error) {
	params, err := publicTurnLookup(tenantID, sessionID, turnID)
	if err != nil {
		return sessions.Turn{}, err
	}
	row, err := s.queries.GetTurn(ctx, params)
	if errors.Is(err, pgx.ErrNoRows) {
		return sessions.Turn{}, sessions.ErrNotFound
	}
	if err != nil {
		return sessions.Turn{}, fmt.Errorf("get turn: %w", err)
	}
	return sessionpg.TurnFromRow(row), nil
}

// TransitionTurn is a compare-and-set for execution callbacks. Once terminal,
// a Turn cannot be reopened or have its outcome overwritten, including by retries.
// A dispatcher must claim queued -> in_progress before sending work to a daemon.
func (s *Store) TransitionTurn(ctx context.Context, tenantID, sessionID, turnID string, input sessions.TurnTransition) (sessions.Turn, error) {
	params, err := sessionpg.TurnLookup(tenantID, sessionID, turnID)
	if err != nil {
		return sessions.Turn{}, err
	}
	if !sessions.ValidTransition(input.ExpectedStatus, input.Status) || len(input.Outcome) > 512*1024 {
		return sessions.Turn{}, fmt.Errorf("%w: invalid turn transition or outcome size", sessions.ErrInvalidInput)
	}
	outcome, err := jsonobject.Normalize(input.Outcome)
	if err != nil {
		return sessions.Turn{}, fmt.Errorf("%w: %w", sessions.ErrInvalidInput, err)
	}
	if !sessions.TerminalStatus(input.Status) && string(outcome) != "{}" {
		return sessions.Turn{}, fmt.Errorf("%w: outcome requires a terminal status", sessions.ErrInvalidInput)
	}
	input.Outcome = outcome
	var row sqlc.Turn
	err = s.withSession(ctx, tenantID, sessionID, func(ctx context.Context, q *sqlc.Queries, _ pgtype.UUID) error {
		var err error
		row, err = transitionTurn(ctx, q, params, input)
		return err
	})
	if err != nil {
		return sessions.Turn{}, fmt.Errorf("transition turn: %w", err)
	}
	return sessionpg.TurnFromRow(row), nil
}

func transitionTurn(ctx context.Context, q *sqlc.Queries, params sqlc.GetTurnParams, input sessions.TurnTransition) (sqlc.Turn, error) {
	if _, err := q.GetTurn(ctx, params); errors.Is(err, pgx.ErrNoRows) {
		return sqlc.Turn{}, sessions.ErrNotFound
	} else if err != nil {
		return sqlc.Turn{}, err
	}
	if input.ExpectedStatus == sessions.TurnQueued && input.Status == sessions.TurnInProgress {
		if err := sessions.CheckComputeAdmission(ctx, sessionpg.BindSession(q, params.TenantID, params.SessionID)); err != nil {
			return sqlc.Turn{}, err
		}
	}
	row, err := q.TransitionTurn(ctx, sqlc.TransitionTurnParams{
		ID: params.ID, SessionID: params.SessionID, ExpectedStatus: input.ExpectedStatus,
		NewStatus: input.Status, Outcome: input.Outcome,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return sqlc.Turn{}, sessions.ErrTurnConflict
	}
	if err != nil {
		return sqlc.Turn{}, err
	}
	if !sessions.TerminalStatus(row.Status) {
		if err := sessionpg.AppendChanges(ctx, q, row.SessionID, sessions.TurnChanges(sessionpg.TurnFromRow(row), false)...); err != nil {
			return sqlc.Turn{}, err
		}
		return row, nil
	}
	outcome := sessions.Source{Turn: uuid.UUID(row.ID.Bytes).String(), Kind: "execution_" + row.Status, Payload: row.Outcome, CreatedAt: row.CompletedAt.Time}
	if err = sessions.ProjectSource(ctx, sessionpg.BindSession(q, params.TenantID, row.SessionID), outcome); err != nil {
		return sqlc.Turn{}, err
	}
	if row, err = q.GetTurn(ctx, params); err != nil {
		return sqlc.Turn{}, err
	}
	ending, err := sessionpg.LoadEnding(ctx, q, row.SessionID, row.ID)
	if err != nil {
		return sqlc.Turn{}, err
	}
	if err := sessionpg.ApplyTurnEnd(ctx, q, row.SessionID, row.ID, sessions.EndTurn(sessionpg.TurnFromRow(row), ending)); err != nil {
		return sqlc.Turn{}, err
	}
	return row, nil
}

// publicTurnLookup resolves caller-supplied path identifiers for a Turn or a
// Turn-scoped resource. Unparsable values are indistinguishable from missing ones.
func publicTurnLookup(tenantID, sessionID, turnID string) (sqlc.GetTurnParams, error) {
	tenant, err := parseID(tenantID)
	return sqlc.GetTurnParams{TenantID: tenant, SessionID: pgunit.PathID(sessionID), ID: pgunit.PathID(turnID)}, err
}
