package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// RecordFunctionCall commits an execution callback and its required-action state together.
func (s *Store) RecordFunctionCall(ctx context.Context, tenantID, sessionID, turnID string, call sessions.FunctionCall) error {
	p, err := turnLookup(tenantID, sessionID, turnID)
	if err != nil {
		return err
	}
	if !validFunctionIdentity(call.CallID) || !validFunctionIdentity(call.ExecutorCallID) || !validFunctionIdentity(call.Name) || len(call.Arguments) > 512*1024 || !json.Valid(call.Arguments) || call.Result != nil || call.Applied {
		return sessions.ErrInvalidInput
	}
	return s.withSession(ctx, tenantID, sessionID, func(ctx context.Context, q *sqlc.Queries, session pgtype.UUID) error {
		turn, err := q.GetTurn(ctx, p)
		if errors.Is(err, pgx.ErrNoRows) {
			return sessions.ErrNotFound
		}
		if err != nil {
			return err
		}
		matches, err := q.MatchFunctionCall(ctx, sqlc.MatchFunctionCallParams{SessionID: session, TurnID: p.ID, CallID: call.CallID, ExecutorCallID: call.ExecutorCallID, Name: call.Name, Arguments: call.Arguments})
		if err == nil {
			if !matches {
				return sessions.ErrIdempotencyConflict
			}
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if !acceptsFunctionResult(turn) {
			return sessions.ErrTurnConflict
		}
		count, err := q.CreateFunctionCall(ctx, sqlc.CreateFunctionCallParams{SessionID: session, TurnID: p.ID, CallID: call.CallID, ExecutorCallID: call.ExecutorCallID, Name: call.Name, Arguments: call.Arguments})
		if err != nil {
			return err
		}
		if count != 1 {
			return sessions.ErrIdempotencyConflict
		}
		return recordFunctionState(ctx, q, turn)
	})
}

func (s *Store) GetFunctionCall(ctx context.Context, tenantID, sessionID, turnID, callID string) (sessions.FunctionCall, error) {
	p, err := turnLookup(tenantID, sessionID, turnID)
	if err != nil {
		return sessions.FunctionCall{}, err
	}
	if !validFunctionIdentity(callID) {
		return sessions.FunctionCall{}, sessions.ErrInvalidInput
	}
	row, err := s.queries.GetFunctionCall(ctx, sqlc.GetFunctionCallParams{TenantID: p.TenantID, SessionID: p.SessionID, TurnID: p.ID, CallID: callID})
	if errors.Is(err, pgx.ErrNoRows) {
		return sessions.FunctionCall{}, sessions.ErrNotFound
	}
	if err != nil {
		return sessions.FunctionCall{}, err
	}
	return functionCallFromRow(row), nil
}

// PendingFunctionCalls excludes applied results and cancelling or terminal Turns.
func (s *Store) PendingFunctionCalls(ctx context.Context, tenantID, sessionID, turnID string) ([]sessions.FunctionCall, error) {
	p, err := turnLookup(tenantID, sessionID, turnID)
	if err != nil {
		return nil, err
	}
	result := make([]sessions.FunctionCall, 0)
	err = s.withSession(ctx, tenantID, sessionID, func(ctx context.Context, q *sqlc.Queries, session pgtype.UUID) error {
		if _, err := q.GetTurn(ctx, p); errors.Is(err, pgx.ErrNoRows) {
			return sessions.ErrNotFound
		} else if err != nil {
			return err
		}
		rows, err := q.ListPendingFunctionCalls(ctx, sqlc.ListPendingFunctionCallsParams{SessionID: session, TurnID: p.ID})
		if err != nil {
			return err
		}
		for _, row := range rows {
			result = append(result, functionCallFromRow(row))
		}
		return nil
	})
	return result, err
}

func functionCallFromRow(row sqlc.FunctionCall) sessions.FunctionCall {
	return sessions.FunctionCall{CallID: row.CallID, ExecutorCallID: row.ExecutorCallID, Name: row.Name, Arguments: row.Arguments, Result: row.Result, Applied: row.Applied}
}

func validFunctionIdentity(id string) bool { return strings.TrimSpace(id) != "" && len(id) <= 512 }

func acceptsFunctionResult(turn sqlc.Turn) bool {
	return (turn.Status == sessions.TurnInProgress || turn.Status == sessions.TurnWaiting) && !turn.CancelRequestedAt.Valid
}
