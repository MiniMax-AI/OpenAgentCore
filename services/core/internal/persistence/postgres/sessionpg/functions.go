package sessionpg

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

var (
	_ sessions.FunctionResultTx = (*SessionTx)(nil)
	_ sessions.FunctionTx       = (*SessionTx)(nil)
)

// LoadRequiredActions reads the required actions of the Session's Turn within
// the caller's snapshot, as sessions.LoadRequiredActions selects them.
func LoadRequiredActions(ctx context.Context, q *sqlc.Queries, session pgtype.UUID, turn sessions.Turn) ([]v1.FunctionCallAction, error) {
	return sessions.LoadRequiredActions(ctx, pendingCalls{q: q, session: session}, turn)
}

// pendingCalls reads a Session's pending function calls without the Session
// lock, for a snapshot read.
type pendingCalls struct {
	q       *sqlc.Queries
	session pgtype.UUID
}

func (p pendingCalls) LoadPendingFunctionCalls(ctx context.Context, turn string) ([]sessions.FunctionCall, error) {
	return loadPendingFunctionCalls(ctx, p.q, p.session, turn)
}

func loadPendingFunctionCalls(ctx context.Context, q *sqlc.Queries, session pgtype.UUID, turn string) ([]sessions.FunctionCall, error) {
	id, err := parseID(turn)
	if err != nil {
		return nil, err
	}
	rows, err := q.ListPendingFunctionCalls(ctx, sqlc.ListPendingFunctionCallsParams{SessionID: session, TurnID: id})
	if err != nil {
		return nil, err
	}
	calls := make([]sessions.FunctionCall, 0, len(rows))
	for _, row := range rows {
		calls = append(calls, functionCallFromRow(row))
	}
	return calls, nil
}

func functionCallFromRow(row sqlc.FunctionCall) sessions.FunctionCall {
	return sessions.FunctionCall{CallID: row.CallID, ExecutorCallID: row.ExecutorCallID, Name: row.Name, Arguments: row.Arguments, Result: row.Result, Applied: row.Applied}
}

func (t *SessionTx) LoadPendingFunctionCalls(ctx context.Context, turn string) ([]sessions.FunctionCall, error) {
	return loadPendingFunctionCalls(ctx, t.q, t.session, turn)
}

func (t *SessionTx) FindResultTurn(ctx context.Context, turn string) (sessions.Turn, bool, error) {
	row, err := t.q.SessionEventTurn(ctx, sqlc.SessionEventTurnParams{SessionID: t.session, ID: pgunit.PathID(turn)})
	if errors.Is(err, pgx.ErrNoRows) {
		return sessions.Turn{}, false, nil
	}
	if err != nil {
		return sessions.Turn{}, false, err
	}
	return TurnFromRow(row), true, nil
}

func (t *SessionTx) MatchFunctionResult(ctx context.Context, turn, call string, result json.RawMessage) (sessions.FunctionResultMatch, error) {
	id, err := parseID(turn)
	if err != nil {
		return sessions.FunctionResultMatch{}, err
	}
	row, err := t.q.MatchFunctionResult(ctx, sqlc.MatchFunctionResultParams{SessionID: t.session, TurnID: id, CallID: call, Result: result})
	if errors.Is(err, pgx.ErrNoRows) {
		return sessions.FunctionResultMatch{}, nil
	}
	if err != nil {
		return sessions.FunctionResultMatch{}, err
	}
	return sessions.FunctionResultMatch{Recorded: true, Submitted: row.Submitted, Matches: row.Matches}, nil
}

func (t *SessionTx) SubmitFunctionResult(ctx context.Context, turn, call string, result json.RawMessage) error {
	id, err := parseID(turn)
	if err != nil {
		return err
	}
	return t.q.SubmitFunctionResult(ctx, sqlc.SubmitFunctionResultParams{SessionID: t.session, TurnID: id, CallID: call, Result: result})
}

func (t *SessionTx) HasFunctionCall(ctx context.Context, call string) (bool, error) {
	return t.q.SessionHasFunctionCall(ctx, sqlc.SessionHasFunctionCallParams{SessionID: t.session, CallID: call})
}

func (t *SessionTx) MatchFunctionCall(ctx context.Context, turn string, call sessions.FunctionCall) (sessions.FunctionCallMatch, error) {
	id, err := parseID(turn)
	if err != nil {
		return sessions.FunctionCallMatch{}, err
	}
	matches, err := t.q.MatchFunctionCall(ctx, sqlc.MatchFunctionCallParams{SessionID: t.session, TurnID: id, CallID: call.CallID, ExecutorCallID: call.ExecutorCallID, Name: call.Name, Arguments: call.Arguments})
	if errors.Is(err, pgx.ErrNoRows) {
		return sessions.FunctionCallMatch{}, nil
	}
	if err != nil {
		return sessions.FunctionCallMatch{}, err
	}
	return sessions.FunctionCallMatch{Recorded: true, Matches: matches}, nil
}

func (t *SessionTx) CreateFunctionCall(ctx context.Context, turn string, call sessions.FunctionCall) error {
	id, err := parseID(turn)
	if err != nil {
		return err
	}
	count, err := t.q.CreateFunctionCall(ctx, sqlc.CreateFunctionCallParams{SessionID: t.session, TurnID: id, CallID: call.CallID, ExecutorCallID: call.ExecutorCallID, Name: call.Name, Arguments: call.Arguments})
	if err != nil {
		return err
	}
	if count != 1 {
		return sessions.ErrIdempotencyConflict
	}
	return nil
}

func (t *SessionTx) LoadFunctionCall(ctx context.Context, turn, call string) (sessions.FunctionCall, bool, error) {
	id, err := parseID(turn)
	if err != nil {
		return sessions.FunctionCall{}, false, err
	}
	row, err := t.q.GetFunctionCall(ctx, sqlc.GetFunctionCallParams{TenantID: t.tenant, SessionID: t.session, TurnID: id, CallID: call})
	if errors.Is(err, pgx.ErrNoRows) {
		return sessions.FunctionCall{}, false, nil
	}
	if err != nil {
		return sessions.FunctionCall{}, false, err
	}
	return functionCallFromRow(row), true, nil
}

func (t *SessionTx) ApplyFunctionResult(ctx context.Context, turn, call string) error {
	id, err := parseID(turn)
	if err != nil {
		return err
	}
	return t.q.ApplyFunctionResult(ctx, sqlc.ApplyFunctionResultParams{SessionID: t.session, TurnID: id, CallID: call})
}

func (t *SessionTx) ApplyFunctionTurnStatus(ctx context.Context, turn, expected, status string) (sessions.Turn, error) {
	id, err := parseID(turn)
	if err != nil {
		return sessions.Turn{}, err
	}
	row, err := t.q.TransitionTurn(ctx, sqlc.TransitionTurnParams{ID: id, SessionID: t.session, ExpectedStatus: expected, NewStatus: status, Outcome: []byte(`{}`)})
	if errors.Is(err, pgx.ErrNoRows) {
		return sessions.Turn{}, sessions.ErrTurnConflict
	}
	if err != nil {
		return sessions.Turn{}, err
	}
	return TurnFromRow(row), nil
}
