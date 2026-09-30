package store

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// Result targets are resolved only inside a tenant-owned Session, after its
// lookup, so a missing or foreign Session still returns ErrNotFound (EVT-11).
var (
	// ErrUnknownFunctionCall rejects a result whose call_id names no function
	// call in the Session.
	ErrUnknownFunctionCall = errors.New("unknown pending tool call")
	// ErrFunctionCallTurnMismatch rejects a result whose call exists in the
	// Session but not in the named Turn, including a malformed or unknown Turn.
	ErrFunctionCallTurnMismatch = errors.New("tool call belongs to a different turn")
)

// FunctionResultInput identifies a persisted call; Result is validated by the API.
// It is an internal command, not an upstream input event. TurnID is the caller's
// value and is resolved within the Session at admission.
type FunctionResultInput struct {
	TurnID string          `json:"turn_id"`
	CallID string          `json:"call_id"`
	Result json.RawMessage `json:"result"`
}

func functionInput(raw json.RawMessage) (FunctionResultInput, error) {
	var input FunctionResultInput
	if json.Unmarshal(raw, &input) != nil || input.TurnID == "" || !validFunctionIdentity(input.CallID) || len(input.Result) == 0 {
		return input, ErrInvalidInput
	}
	result, err := canonicalJSONObject(input.Result)
	if err != nil {
		return input, err
	}
	input.Result = result
	return input, nil
}

func admitFunctionResult(ctx context.Context, q *sqlc.Queries, tenantID string, session pgtype.UUID, key string, position int32, input Input) (InputReceipt, error) {
	result, err := functionInput(input.Payload)
	if err != nil {
		return InputReceipt{}, err
	}
	tenant, err := parseID(tenantID)
	if err != nil {
		return InputReceipt{}, err
	}
	var turn sqlc.Turn
	found := false
	// A malformed Turn ID cannot name a Turn; it is classified like an unknown one.
	if id, err := parseID(result.TurnID); err == nil {
		turn, err = q.GetTurn(ctx, sqlc.GetTurnParams{TenantID: tenant, SessionID: session, ID: id})
		if err == nil {
			found = true
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return InputReceipt{}, err
		}
	}
	if found {
		err = storeFunctionResult(ctx, q, turn, result.CallID, result.Result)
		if errors.Is(err, ErrNotFound) {
			found = false
		} else if err != nil {
			return InputReceipt{}, err
		}
	}
	if !found {
		return InputReceipt{}, unknownFunctionResultTarget(ctx, q, session, result.CallID)
	}
	sequence, err := q.CreateTurnInput(ctx, sqlc.CreateTurnInputParams{
		SessionID: session, TurnID: turn.ID, IdempotencyKey: key, Kind: input.Kind, Payload: input.Payload, BatchPosition: position,
	})
	if err != nil {
		return InputReceipt{}, err
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
		return ErrFunctionCallTurnMismatch
	}
	return ErrUnknownFunctionCall
}
