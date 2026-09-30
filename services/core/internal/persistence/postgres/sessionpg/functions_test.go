package sessionpg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgtest"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

// functionTurn is a stored tenant's Session with one Turn.
type functionTurn struct {
	tenant, session, turn pgtype.UUID
}

func (f functionTurn) ids() (string, string, string) {
	return uuid.UUID(f.tenant.Bytes).String(), uuid.UUID(f.session.Bytes).String(), uuid.UUID(f.turn.Bytes).String()
}

func (f functionTurn) turnID() string { return uuid.UUID(f.turn.Bytes).String() }

// newFunctionTurn stores a fresh tenant's Session with one Turn of status.
func newFunctionTurn(t *testing.T, pool *pgxpool.Pool, status string) functionTurn {
	t.Helper()
	tenant, session := uuid.New(), uuid.New()
	exec(t, pool, `INSERT INTO sessions(id, tenant_id, engine, idempotency_key, request_hash) VALUES ($1, $2, 'codex', 'key', 'hash')`, session, tenant)
	f := functionTurn{tenant: pgID(tenant), session: pgID(session)}
	f.turn = addTurn(t, pool, f.session, status)
	return f
}

// addTurn stores another Turn of status in the Session.
func addTurn(t *testing.T, pool *pgxpool.Pool, session pgtype.UUID, status string) pgtype.UUID {
	t.Helper()
	turn := uuid.New()
	exec(t, pool, `INSERT INTO turns(id, session_id, status, completed_at)
		VALUES ($1, $2, $3::text, CASE WHEN $3::text IN ('completed', 'failed', 'cancelled') THEN clock_timestamp() END)`, turn, session, status)
	return pgID(turn)
}

// storeCall stores a function call of the Turn, with result when it is not
// empty.
func storeCall(t *testing.T, pool *pgxpool.Pool, session, turn pgtype.UUID, call, result string) {
	t.Helper()
	var saved any
	if result != "" {
		saved = json.RawMessage(result)
	}
	exec(t, pool, `INSERT INTO function_calls(session_id, turn_id, call_id, executor_call_id, name, arguments, result) VALUES ($1, $2, $3, 'native-' || $3, 'lookup', '{}', $4::jsonb)`, session, turn, call, saved)
}

// storedCall reads a stored function call.
func storedCall(t *testing.T, pool *pgxpool.Pool, f functionTurn, turn pgtype.UUID, call string) sqlc.FunctionCall {
	t.Helper()
	row, err := sqlc.New(pool).GetFunctionCall(t.Context(), sqlc.GetFunctionCallParams{TenantID: f.tenant, SessionID: f.session, TurnID: turn, CallID: call})
	if err != nil {
		t.Fatal(err)
	}
	return row
}

// admitResult admits a function result in its own pooled Session transaction,
// as input admission does.
func admitResult(ctx context.Context, pool *pgxpool.Pool, f functionTurn, turn, call, result string) (sessions.Turn, error) {
	var admitted sessions.Turn
	err := WithSession(ctx, pgunit.NewPool(pool), f.tenant, f.session, func(ctx context.Context, q *sqlc.Queries, _ sessions.LockedSession) error {
		var err error
		admitted, err = sessions.AdmitFunctionResult(ctx, BindSession(q, f.tenant, f.session), sessions.FunctionResultInput{TurnID: turn, CallID: call, Result: json.RawMessage(result)})
		return err
	})
	return admitted, err
}

func TestAdmitFunctionResultThroughTheBinding(t *testing.T) {
	pool := pgtest.Open(t)
	f := newFunctionTurn(t, pool, sessions.TurnWaiting)
	storeCall(t, pool, f.session, f.turn, "call", "")
	other := addTurn(t, pool, f.session, sessions.TurnCompleted)
	storeCall(t, pool, f.session, other, "earlier", `{"success":true}`)
	storeCall(t, pool, f.session, other, "late", "")
	// Another Session's calls never classify this Session's results.
	foreign := newFunctionTurn(t, pool, sessions.TurnWaiting)
	storeCall(t, pool, foreign.session, foreign.turn, "foreign", "")

	turn, err := admitResult(t.Context(), pool, f, f.turnID(), "call", `{"success":true,"output":"saved"}`)
	if err != nil || turn.ID != f.turnID() || turn.Status != sessions.TurnWaiting {
		t.Fatal(turn, err)
	}
	if row := storedCall(t, pool, f, f.turn, "call"); string(row.Result) != `{"output": "saved", "success": true}` || row.Applied {
		t.Fatalf("stored %s", row.Result)
	}
	if _, err := admitResult(t.Context(), pool, f, f.turnID(), "call", `{"output":"saved","success":true}`); err != nil {
		t.Fatal("identical retry", err)
	}
	if _, err := admitResult(t.Context(), pool, f, f.turnID(), "call", `{"success":false}`); !errors.Is(err, sessions.ErrFunctionResultConflict) {
		t.Fatal("changed result", err)
	}
	// A result saved before the Turn ended still answers an identical retry.
	if _, err := admitResult(t.Context(), pool, f, uuid.UUID(other.Bytes).String(), "earlier", `{"success":true}`); err != nil {
		t.Fatal("retry after the Turn ended", err)
	}
	for name, test := range map[string]struct {
		turn, call string
		want       error
	}{
		"call of another Turn":                {f.turnID(), "earlier", sessions.ErrFunctionCallTurnMismatch},
		"unknown call":                        {f.turnID(), "missing", sessions.ErrUnknownFunctionCall},
		"unknown Turn":                        {uuid.NewString(), "call", sessions.ErrFunctionCallTurnMismatch},
		"malformed Turn":                      {"not-a-turn", "call", sessions.ErrFunctionCallTurnMismatch},
		"malformed Turn and unknown call":     {"not-a-turn", "missing", sessions.ErrUnknownFunctionCall},
		"another Session's call":              {f.turnID(), "foreign", sessions.ErrUnknownFunctionCall},
		"another Session's Turn and call":     {foreign.turnID(), "foreign", sessions.ErrUnknownFunctionCall},
		"another Session's Turn, a call here": {foreign.turnID(), "call", sessions.ErrFunctionCallTurnMismatch},
		"new result for an ended Turn":        {uuid.UUID(other.Bytes).String(), "late", sessions.ErrTurnConflict},
	} {
		if _, err := admitResult(t.Context(), pool, f, test.turn, test.call, `{"success":true}`); !errors.Is(err, test.want) {
			t.Errorf("%s: %v", name, err)
		}
	}

	// A Turn whose cancellation was requested takes no new result.
	storeCall(t, pool, f.session, f.turn, "pending", "")
	exec(t, pool, `UPDATE turns SET cancel_requested_at = clock_timestamp() WHERE id = $1`, f.turn)
	if _, err := admitResult(t.Context(), pool, f, f.turnID(), "pending", `{"success":true}`); !errors.Is(err, sessions.ErrTurnConflict) {
		t.Fatal("cancelling Turn", err)
	}
	if row := storedCall(t, pool, f, f.turn, "pending"); row.Result != nil {
		t.Fatalf("stored %s", row.Result)
	}
}

func TestAdmitFunctionResultRollsBackWithItsTransaction(t *testing.T) {
	pool := pgtest.Open(t)
	f := newFunctionTurn(t, pool, sessions.TurnWaiting)
	storeCall(t, pool, f.session, f.turn, "call", "")
	err := WithSession(t.Context(), pgunit.NewPool(pool), f.tenant, f.session, func(ctx context.Context, q *sqlc.Queries, _ sessions.LockedSession) error {
		if _, err := sessions.AdmitFunctionResult(ctx, BindSession(q, f.tenant, f.session), sessions.FunctionResultInput{TurnID: f.turnID(), CallID: "call", Result: json.RawMessage(`{"success":true}`)}); err != nil {
			return err
		}
		return errRollback
	})
	if row := storedCall(t, pool, f, f.turn, "call"); !errors.Is(err, errRollback) || row.Result != nil {
		t.Fatalf("rolled back result kept: %s %v", row.Result, err)
	}
}

func TestConcurrentFunctionResultsChooseOneValue(t *testing.T) {
	pool := pgtest.Open(t)
	f := newFunctionTurn(t, pool, sessions.TurnWaiting)
	storeCall(t, pool, f.session, f.turn, "call", "")
	var wg sync.WaitGroup
	outcomes := make(chan error, 2)
	for _, output := range []string{"first", "second"} {
		wg.Go(func() {
			_, err := admitResult(t.Context(), pool, f, f.turnID(), "call", fmt.Sprintf(`{"success":true,"output":%q}`, output))
			outcomes <- err
		})
	}
	wg.Wait()
	close(outcomes)
	winners, conflicts := 0, 0
	for err := range outcomes {
		switch {
		case err == nil:
			winners++
		case errors.Is(err, sessions.ErrFunctionResultConflict):
			conflicts++
		default:
			t.Fatal(err)
		}
	}
	if winners != 1 || conflicts != 1 {
		t.Fatal(winners, conflicts)
	}
}

func TestLoadRequiredActionsReadsPendingCalls(t *testing.T) {
	pool := pgtest.Open(t)
	q := sqlc.New(pool)
	f := newFunctionTurn(t, pool, sessions.TurnWaiting)
	for _, call := range []string{"b", "a", "applied"} {
		storeCall(t, pool, f.session, f.turn, call, "")
	}
	exec(t, pool, `UPDATE function_calls SET created_at = '2026-01-01', arguments = '{"ticket":9007199254740993}' WHERE session_id = $1 AND call_id IN ('a', 'b')`, f.session)
	exec(t, pool, `UPDATE function_calls SET result = '{}', applied = true WHERE session_id = $1 AND call_id = 'applied'`, f.session)
	row, err := q.GetTurn(t.Context(), sqlc.GetTurnParams{TenantID: f.tenant, SessionID: f.session, ID: f.turn})
	if err != nil {
		t.Fatal(err)
	}
	actions, err := LoadRequiredActions(t.Context(), q, f.session, TurnFromRow(row))
	if err != nil || len(actions) != 2 || actions[0].CallID != "a" || actions[1].CallID != "b" {
		t.Fatalf("actions %+v %v", actions, err)
	}
	encoded, err := json.Marshal(actions[0])
	if err != nil || string(encoded) != `{"arguments":{"ticket":9007199254740993},"call_id":"a","name":"lookup","turn_id":"`+f.turnID()+`","type":"function_call"}` {
		t.Fatal(string(encoded), err)
	}
	exec(t, pool, `UPDATE turns SET cancel_requested_at = clock_timestamp() WHERE id = $1`, f.turn)
	// The Turn as the caller read it still waits; the read of its calls sees
	// the requested cancellation.
	if actions, err := LoadRequiredActions(t.Context(), q, f.session, TurnFromRow(row)); err != nil || actions == nil || len(actions) != 0 {
		t.Fatalf("cancelling Turn actions %+v %v", actions, err)
	}
}
