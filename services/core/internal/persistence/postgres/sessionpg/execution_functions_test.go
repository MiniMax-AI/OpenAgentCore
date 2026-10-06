package sessionpg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/jsonobject"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgtest"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

// sessionExecution takes the execution lease of pool's database and builds
// the Session execution operations on it. The lease closes when the test ends.
func sessionExecution(t *testing.T, pool *pgxpool.Pool) (*sessions.ExecutionOperations, *pgunit.Lease) {
	t.Helper()
	lease, err := pgunit.AcquireLease(t.Context(), pool)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lease.Close(context.Background()) })
	operations, err := sessions.NewExecutionOperations(NewExecution(lease))
	if err != nil {
		t.Fatal(err)
	}
	return operations, lease
}

func functionCall(id string) sessions.FunctionCall {
	return sessions.FunctionCall{CallID: id, ExecutorCallID: "native-" + id, Name: "lookup", Arguments: json.RawMessage(`{"ticket":9007199254740993}`)}
}

func turnStatus(t *testing.T, pool *pgxpool.Pool, f functionTurn) string {
	t.Helper()
	var status string
	if err := pool.QueryRow(t.Context(), `SELECT status FROM turns WHERE id = $1`, f.turn).Scan(&status); err != nil {
		t.Fatal(err)
	}
	return status
}

// Calls, results and receipts commit on the lease, survive a new execution
// owner, and journal the Turn's required actions after each change.
func TestFunctionExecutionRecordsCallsAndReceipts(t *testing.T) {
	pool := pgtest.OpenIsolated(t, nil)
	operations, lease := sessionExecution(t, pool)
	f := newFunctionTurn(t, pool, sessions.TurnInProgress)
	tenant, session, turn := f.ids()
	results := []string{
		`{"success":true,"output":"answer"}`,
		`{"success":true,"output":""}`,
		`{"success":false,"output":[{"type":"input_text","text":"before"},{"type":"input_image","image_url":"data:image/png;base64,AA=="},{"type":"input_text","text":""}],"error":"tool failed"}`,
		`{"success":true,"output":[],"error":null}`,
		`{"success":false,"output":null,"error":"missing"}`,
		`{"success":false}`,
	}
	for i, raw := range results {
		call := functionCall(fmt.Sprint(i))
		if err := operations.RecordFunctionCall(t.Context(), tenant, session, turn, call); err != nil {
			t.Fatal(err)
		}
		retry := call
		retry.Arguments = json.RawMessage(`{ "ticket" : 9007199254740993 }`)
		if err := operations.RecordFunctionCall(t.Context(), tenant, session, turn, retry); err != nil {
			t.Fatal("reformatted retry", err)
		}
		if err := operations.ConfirmFunctionResult(t.Context(), tenant, session, turn, call.CallID); !errors.Is(err, sessions.ErrTurnConflict) {
			t.Fatal("unsubmitted result applied", err)
		}
		if _, err := admitResult(t.Context(), pool, f, turn, call.CallID, raw); err != nil {
			t.Fatal(err)
		}
	}
	if status := turnStatus(t, pool, f); status != sessions.TurnWaiting {
		t.Fatal(status)
	}
	before, err := operations.PendingFunctionCalls(t.Context(), tenant, session, turn)
	if err != nil || len(before) != len(results) {
		t.Fatal(before, err)
	}

	// A new execution owner reads the same calls and results.
	awaitRelease := pgtest.ObserveExecutionLeaseRelease(t, pool)
	if err := lease.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	awaitRelease()
	if err := operations.RecordFunctionCall(t.Context(), tenant, session, turn, functionCall("late")); !errors.Is(err, pgunit.ErrLeaseClosed) {
		t.Fatal("closed lease recorded a call", err)
	}
	operations, _ = sessionExecution(t, pool)
	restored, err := operations.PendingFunctionCalls(t.Context(), tenant, session, turn)
	if err != nil || !reflect.DeepEqual(restored, before) {
		t.Fatal("recovery lost calls or results", restored, err)
	}
	for i, expected := range results {
		id := fmt.Sprint(i)
		call := restored[i]
		if call.CallID != id || call.Applied || call.ExecutorCallID != "native-"+id || !strings.Contains(string(call.Arguments), "9007199254740993") {
			t.Fatal(call)
		}
		saved, err := jsonobject.Normalize(call.Result)
		wanted, _ := jsonobject.Normalize(json.RawMessage(expected))
		if err != nil || string(saved) != string(wanted) {
			t.Fatal("result changed", string(call.Result), err)
		}
		if _, err := admitResult(t.Context(), pool, f, turn, id, expected); err != nil {
			t.Fatal(err)
		}
		if _, err := admitResult(t.Context(), pool, f, turn, id, `{"success":false,"error":"changed"}`); !errors.Is(err, sessions.ErrFunctionResultConflict) {
			t.Fatal(err)
		}
		for range 2 {
			if err := operations.ConfirmFunctionResult(t.Context(), tenant, session, turn, id); err != nil {
				t.Fatal(err)
			}
		}
	}
	pending, err := operations.PendingFunctionCalls(t.Context(), tenant, session, turn)
	if err != nil || pending == nil || len(pending) != 0 {
		t.Fatal(pending, err)
	}
	if status := turnStatus(t, pool, f); status != sessions.TurnInProgress {
		t.Fatal(status)
	}
	if row := storedCall(t, pool, f, f.turn, "0"); !row.Applied {
		t.Fatal("receipt did not persist")
	}

	// Each change journals the required actions once: six calls, then five
	// receipts that leave actions, then the resumed Turn and Session.
	_, changes := journal(t, pool, f.session)
	want := append(slices.Repeat([]string{"agent.session.requires_action"}, 11), "agent.session.turn.in_progress", "agent.session.in_progress")
	if got := kinds(changes); !slices.Equal(got, want) {
		t.Fatalf("journal %v", got)
	}
	for i, change := range changes[:11] {
		count := i + 1
		if i >= 6 {
			count = 11 - i
		}
		if len(change.RequiredActions) != count || change.Turn == nil || change.Turn.Status != sessions.TurnWaiting {
			t.Fatalf("change %d: %+v", i, change)
		}
	}
	var precise int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM session_events WHERE session_id = $1 AND payload::text LIKE '%"ticket": 9007199254740993%'`, f.session).Scan(&precise); err != nil || precise != 11 {
		t.Fatal("argument precision lost", precise, err)
	}
}

// Function calls are scoped to the tenant's Turn, keep their identities, and
// change only while the Turn accepts results.
func TestFunctionExecutionScopesAndRejects(t *testing.T) {
	pool := pgtest.OpenIsolated(t, nil)
	operations, _ := sessionExecution(t, pool)

	t.Run("identities are immutable", func(t *testing.T) {
		f := newFunctionTurn(t, pool, sessions.TurnQueued)
		tenant, session, turn := f.ids()
		call := functionCall("call")
		if err := operations.RecordFunctionCall(t.Context(), tenant, session, turn, call); !errors.Is(err, sessions.ErrTurnConflict) {
			t.Fatal("queued Turn accepted a call", err)
		}
		exec(t, pool, `UPDATE turns SET status = 'in_progress' WHERE id = $1`, f.turn)
		if err := operations.RecordFunctionCall(t.Context(), tenant, session, turn, call); err != nil {
			t.Fatal(err)
		}
		for field, change := range map[string]func(*sessions.FunctionCall){
			"call":      func(c *sessions.FunctionCall) { c.CallID = "another-public-id" },
			"executor":  func(c *sessions.FunctionCall) { c.ExecutorCallID = "another-executor-id" },
			"name":      func(c *sessions.FunctionCall) { c.Name = "another-function" },
			"arguments": func(c *sessions.FunctionCall) { c.Arguments = json.RawMessage(`{"ticket":9007199254740992}`) },
		} {
			changed := call
			change(&changed)
			if err := operations.RecordFunctionCall(t.Context(), tenant, session, turn, changed); !errors.Is(err, sessions.ErrIdempotencyConflict) {
				t.Fatal(field, err)
			}
		}
		// The same public identity is a new call in another Turn.
		exec(t, pool, `UPDATE turns SET status = 'completed', completed_at = clock_timestamp() WHERE id = $1`, f.turn)
		next := f
		next.turn = addTurn(t, pool, f.session, sessions.TurnInProgress)
		if err := operations.RecordFunctionCall(t.Context(), tenant, session, next.turnID(), call); err != nil {
			t.Fatal("call identity leaked across Turns", err)
		}
	})

	t.Run("lookups are scoped", func(t *testing.T) {
		f := newFunctionTurn(t, pool, sessions.TurnInProgress)
		tenant, session, turn := f.ids()
		if err := operations.RecordFunctionCall(t.Context(), tenant, session, turn, functionCall("call")); err != nil {
			t.Fatal(err)
		}
		for _, scope := range []struct{ tenant, session, turn string }{
			{uuid.NewString(), session, turn}, {tenant, uuid.NewString(), turn}, {tenant, session, uuid.NewString()},
		} {
			if _, err := operations.PendingFunctionCalls(t.Context(), scope.tenant, scope.session, scope.turn); !errors.Is(err, sessions.ErrNotFound) {
				t.Fatal(err)
			}
			if err := operations.RecordFunctionCall(t.Context(), scope.tenant, scope.session, scope.turn, functionCall("call")); !errors.Is(err, sessions.ErrNotFound) {
				t.Fatal(err)
			}
			if err := operations.ConfirmFunctionResult(t.Context(), scope.tenant, scope.session, scope.turn, "call"); !errors.Is(err, sessions.ErrNotFound) {
				t.Fatal(err)
			}
		}
		for _, scope := range []struct{ tenant, session, turn string }{
			{"tenant", session, turn}, {tenant, "session", turn}, {tenant, session, "turn"},
		} {
			if _, err := operations.PendingFunctionCalls(t.Context(), scope.tenant, scope.session, scope.turn); !errors.Is(err, sessions.ErrInvalidInput) {
				t.Fatal(err)
			}
		}
		if err := operations.ConfirmFunctionResult(t.Context(), tenant, session, turn, "missing"); !errors.Is(err, sessions.ErrNotFound) {
			t.Fatal(err)
		}
		// A publicly deleted Session still settles its Turn's calls.
		exec(t, pool, `UPDATE sessions SET deleted_at = clock_timestamp() WHERE id = $1`, f.session)
		if pending, err := operations.PendingFunctionCalls(t.Context(), tenant, session, turn); err != nil || len(pending) != 1 {
			t.Fatal(pending, err)
		}
	})

	for _, end := range []string{"cancelling", sessions.TurnCancelled, sessions.TurnCompleted, sessions.TurnFailed} {
		t.Run("no changes after "+end, func(t *testing.T) {
			f := newFunctionTurn(t, pool, sessions.TurnInProgress)
			tenant, session, turn := f.ids()
			for _, id := range []string{"submitted", "pending"} {
				if err := operations.RecordFunctionCall(t.Context(), tenant, session, turn, functionCall(id)); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := admitResult(t.Context(), pool, f, turn, "submitted", `{"success":true,"output":"saved"}`); err != nil {
				t.Fatal(err)
			}
			if end == "cancelling" {
				exec(t, pool, `UPDATE turns SET cancel_requested_at = clock_timestamp() WHERE id = $1`, f.turn)
			} else {
				exec(t, pool, `UPDATE turns SET status = $2, completed_at = clock_timestamp() WHERE id = $1`, f.turn, end)
			}
			if pending, err := operations.PendingFunctionCalls(t.Context(), tenant, session, turn); err != nil || len(pending) != 0 {
				t.Fatal(pending, err)
			}
			if err := operations.ConfirmFunctionResult(t.Context(), tenant, session, turn, "submitted"); !errors.Is(err, sessions.ErrTurnConflict) {
				t.Fatal(err)
			}
			if err := operations.RecordFunctionCall(t.Context(), tenant, session, turn, functionCall("late")); !errors.Is(err, sessions.ErrTurnConflict) {
				t.Fatal(err)
			}
			if row := storedCall(t, pool, f, f.turn, "submitted"); row.Applied || len(row.Result) == 0 {
				t.Fatal("saved result lost or falsely applied")
			}
		})
	}
}
