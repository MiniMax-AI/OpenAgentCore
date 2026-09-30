package store

import (
	"context"
	"encoding/json"
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
	return turnFromRow(row), nil
}

// TransitionTurn is a compare-and-set for execution callbacks. Once terminal,
// a Turn cannot be reopened or have its outcome overwritten, including by retries.
// A dispatcher must claim queued -> in_progress before sending work to a daemon.
func (s *Store) TransitionTurn(ctx context.Context, tenantID, sessionID, turnID string, input sessions.TurnTransition) (sessions.Turn, error) {
	params, err := turnLookup(tenantID, sessionID, turnID)
	if err != nil {
		return sessions.Turn{}, err
	}
	if !validTransition(input.ExpectedStatus, input.Status) || len(input.Outcome) > 512*1024 {
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
	return turnFromRow(row), nil
}

func transitionTurn(ctx context.Context, q *sqlc.Queries, params sqlc.GetTurnParams, input sessions.TurnTransition) (sqlc.Turn, error) {
	if _, err := q.GetTurn(ctx, params); errors.Is(err, pgx.ErrNoRows) {
		return sqlc.Turn{}, sessions.ErrNotFound
	} else if err != nil {
		return sqlc.Turn{}, err
	}
	if input.ExpectedStatus == sessions.TurnQueued && input.Status == sessions.TurnInProgress {
		if err := checkRuntimeComputeAdmission(ctx, q, params.SessionID); err != nil {
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
		if err := sessionpg.AppendChanges(ctx, q, row.SessionID, sessions.TurnChanges(turnFromRow(row), false)...); err != nil {
			return sqlc.Turn{}, err
		}
		return row, nil
	}
	if err = projectSource(ctx, q, row.SessionID, row.ID, "execution_"+row.Status, 0, row.Outcome, row.CompletedAt); err != nil {
		return sqlc.Turn{}, err
	}
	if row, err = q.GetTurn(ctx, params); err != nil {
		return sqlc.Turn{}, err
	}
	ending, err := sessionpg.LoadEnding(ctx, q, row.SessionID, row.ID)
	if err != nil {
		return sqlc.Turn{}, err
	}
	if err := sessionpg.ApplyTurnEnd(ctx, q, row.SessionID, row.ID, sessions.EndTurn(turnFromRow(row), ending)); err != nil {
		return sqlc.Turn{}, err
	}
	return row, nil
}

func validTransition(from, to string) bool {
	switch from {
	case sessions.TurnQueued:
		return to == sessions.TurnInProgress || to == sessions.TurnFailed || to == sessions.TurnCancelled
	case sessions.TurnInProgress:
		return to == sessions.TurnWaiting || sessions.TerminalStatus(to)
	case sessions.TurnWaiting:
		return to == sessions.TurnInProgress || sessions.TerminalStatus(to)
	default:
		return false
	}
}

func turnLookup(tenantID, sessionID, turnID string) (sqlc.GetTurnParams, error) {
	var p sqlc.GetTurnParams
	var err error
	if p.TenantID, err = parseID(tenantID); err != nil {
		return p, err
	}
	if p.SessionID, err = parseID(sessionID); err != nil {
		return p, err
	}
	p.ID, err = parseID(turnID)
	return p, err
}

// publicTurnLookup resolves caller-supplied path identifiers for a Turn or a
// Turn-scoped resource. Unparsable values are indistinguishable from missing ones.
func publicTurnLookup(tenantID, sessionID, turnID string) (sqlc.GetTurnParams, error) {
	tenant, err := parseID(tenantID)
	return sqlc.GetTurnParams{TenantID: tenant, SessionID: pgunit.PathID(sessionID), ID: pgunit.PathID(turnID)}, err
}

func turnFromRow(row sqlc.Turn) sessions.Turn {
	return sessions.Turn{
		ID: uuid.UUID(row.ID.Bytes).String(), SessionID: uuid.UUID(row.SessionID.Bytes).String(), Status: row.Status,
		CreatedAt: row.CreatedAt.Time, StartedAt: row.StartedAt.Time, CompletedAt: row.CompletedAt.Time,
		CancelRequestedAt: row.CancelRequestedAt.Time, Outcome: json.RawMessage(row.Outcome), Usage: json.RawMessage(row.TokenUsage),
		ArtifactCaptureStarted: row.ArtifactCaptureStarted,
	}
}
