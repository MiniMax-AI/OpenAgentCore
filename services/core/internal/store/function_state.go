package store

import (
	"context"
	"encoding/json"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/sessionpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/google/uuid"
)

func functionActions(ctx context.Context, q *sqlc.Queries, turn sqlc.Turn) ([]v1.FunctionCallAction, error) {
	actions := make([]v1.FunctionCallAction, 0)
	if !acceptsFunctionResult(turn) {
		return actions, nil
	}
	calls, err := q.ListPendingFunctionCalls(ctx, sqlc.ListPendingFunctionCallsParams{SessionID: turn.SessionID, TurnID: turn.ID})
	if err != nil {
		return nil, err
	}
	for _, call := range calls {
		actions = append(actions, v1.FunctionCallAction{Type: "function_call", CallID: call.CallID, Name: call.Name, TurnID: uuid.UUID(turn.ID.Bytes).String(), Arguments: json.RawMessage(call.Arguments)})
	}
	return actions, nil
}

func recordFunctionState(ctx context.Context, q *sqlc.Queries, turn sqlc.Turn) error {
	actions, err := functionActions(ctx, q, turn)
	if err != nil {
		return err
	}
	status := sessions.TurnInProgress
	if len(actions) > 0 {
		status = sessions.TurnWaiting
	}
	if turn.Status != status {
		turn, err = q.TransitionTurn(ctx, sqlc.TransitionTurnParams{ID: turn.ID, SessionID: turn.SessionID, ExpectedStatus: turn.Status, NewStatus: status, Outcome: []byte(`{}`)})
		if err != nil {
			return err
		}
		if err := sessionpg.AppendChanges(ctx, q, turn.SessionID, sessions.TurnChanges(sessionpg.TurnFromRow(turn), false)...); err != nil {
			return err
		}
	}
	usage, err := sessionpg.LoadUsage(ctx, q, turn.SessionID)
	if err != nil {
		return err
	}
	return sessionpg.AppendChanges(ctx, q, turn.SessionID, sessions.ActivityChange(sessionpg.TurnFromRow(turn), usage, actions))
}
