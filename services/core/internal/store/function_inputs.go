package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/jsonobject"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func functionInput(raw json.RawMessage) (sessions.FunctionResultInput, error) {
	var input sessions.FunctionResultInput
	if json.Unmarshal(raw, &input) != nil || input.TurnID == "" || !validFunctionIdentity(input.CallID) || len(input.Result) == 0 {
		return input, sessions.ErrInvalidInput
	}
	result, err := jsonobject.Normalize(input.Result)
	if err != nil {
		return input, fmt.Errorf("%w: %w", sessions.ErrInvalidInput, err)
	}
	input.Result = result
	return input, nil
}

func admitFunctionResult(ctx context.Context, q *sqlc.Queries, tenantID string, session pgtype.UUID, key string, position int32, input sessions.Input) (sessions.InputReceipt, error) {
	result, err := functionInput(input.Payload)
	if err != nil {
		return sessions.InputReceipt{}, err
	}
	tenant, err := parseID(tenantID)
	if err != nil {
		return sessions.InputReceipt{}, err
	}
	var turn sqlc.Turn
	found := false
	// A malformed Turn ID cannot name a Turn; it is classified like an unknown one.
	if id, err := parseID(result.TurnID); err == nil {
		turn, err = q.GetTurn(ctx, sqlc.GetTurnParams{TenantID: tenant, SessionID: session, ID: id})
		if err == nil {
			found = true
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return sessions.InputReceipt{}, err
		}
	}
	if found {
		err = storeFunctionResult(ctx, q, turn, result.CallID, result.Result)
		if errors.Is(err, sessions.ErrNotFound) {
			found = false
		} else if err != nil {
			return sessions.InputReceipt{}, err
		}
	}
	if !found {
		return sessions.InputReceipt{}, unknownFunctionResultTarget(ctx, q, session, result.CallID)
	}
	sequence, err := q.CreateTurnInput(ctx, sqlc.CreateTurnInputParams{
		SessionID: session, TurnID: turn.ID, IdempotencyKey: key, Kind: input.Kind, Payload: input.Payload, BatchPosition: position,
	})
	if err != nil {
		return sessions.InputReceipt{}, err
	}
	return inputReceipt(sequence, turn.ID, false), nil
}

// unknownFunctionResultTarget classifies a result whose Turn does not own the
// call. The answer depends only on this Session's own calls.
func unknownFunctionResultTarget(ctx context.Context, q *sqlc.Queries, session pgtype.UUID, callID string) error {
	elsewhere, err := q.SessionHasFunctionCall(ctx, sqlc.SessionHasFunctionCallParams{SessionID: session, CallID: callID})
	if err != nil {
		return err
	}
	if elsewhere {
		return sessions.ErrFunctionCallTurnMismatch
	}
	return sessions.ErrUnknownFunctionCall
}
