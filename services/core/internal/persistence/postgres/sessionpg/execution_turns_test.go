package sessionpg

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgtest"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

// transition moves the Turn as the seeder does: the TransitionTurn procedure
// in a pooled Session transaction.
func transition(ctx context.Context, pool *pgxpool.Pool, tenant, session pgtype.UUID, turn string, transition sessions.TurnTransition) (sessions.Turn, error) {
	var moved sessions.Turn
	err := WithSession(ctx, pgunit.NewPool(pool), tenant, session, func(ctx context.Context, q *sqlc.Queries, _ sessions.LockedSession) error {
		var err error
		moved, err = sessions.TransitionTurn(ctx, BindSession(q, tenant, session), turn, transition)
		return err
	})
	return moved, err
}

// Concurrent terminal callbacks commit exactly one outcome, which a late
// callback and a new pool see unchanged.
func TestConcurrentTerminalTransitionsKeepOneOutcome(t *testing.T) {
	pool := pgtest.Open(t)
	tenant, session, id := newTurn(t, pool, sessions.TurnInProgress)
	turn := text(id)
	exec(t, pool, `UPDATE turns SET started_at = clock_timestamp(), cancel_requested_at = clock_timestamp() WHERE id = $1`, id)
	var wg sync.WaitGroup
	winners, errs := make(chan sessions.Turn, 3), make(chan error, 3)
	for _, status := range []string{sessions.TurnCompleted, sessions.TurnFailed, sessions.TurnCancelled} {
		wg.Go(func() {
			outcome := json.RawMessage(`{"reported":"` + status + `"}`)
			moved, err := transition(t.Context(), pool, tenant, session, turn, sessions.TurnTransition{ExpectedStatus: sessions.TurnInProgress, Status: status, Outcome: outcome})
			if err != nil {
				errs <- err
				return
			}
			winners <- moved
		})
	}
	wg.Wait()
	close(winners)
	close(errs)
	if len(winners) != 1 || len(errs) != 2 {
		t.Fatalf("winners=%d errors=%d", len(winners), len(errs))
	}
	for err := range errs {
		if !errors.Is(err, sessions.ErrTurnConflict) {
			t.Fatal(err)
		}
	}
	winner := <-winners
	if winner.CompletedAt.IsZero() || winner.CancelRequestedAt.IsZero() || winner.CompletedAt.Before(winner.StartedAt) {
		t.Fatalf("terminal timestamps: %+v", winner)
	}
	if _, err := transition(t.Context(), pool, tenant, session, turn, sessions.TurnTransition{ExpectedStatus: sessions.TurnInProgress, Status: sessions.TurnFailed, Outcome: json.RawMessage(`{"late":true}`)}); !errors.Is(err, sessions.ErrTurnConflict) {
		t.Fatalf("late terminal callback accepted: %v", err)
	}
	got, err := New(pgunit.NewPool(pgtest.Open(t)), pgtest.CredentialKey(t)).GetTurn(t.Context(), text(tenant), text(session), turn)
	if err != nil || !reflect.DeepEqual(got, winner) {
		t.Fatalf("terminal outcome changed: %+v, %v", got, err)
	}
}

// Artifact capture and completion admit only applied inputs. A completion that
// fails rolls back whole; one that commits records its event, publishes the
// staged Artifacts and journals the Turn's end in order. A closed lease
// writes nothing.
func TestCompleteExecutionSettlesTheTurnOnTheLease(t *testing.T) {
	pool := pgtest.OpenIsolated(t, nil)
	operations, lease := sessionExecution(t, pool)
	_, service := stagingService(t, pool)
	tenant, session, environment, turn := stagingTurn(t, pool, "openai_hosted")
	if err := stage(t, service, tenant, session, turn, environment, bytes.NewReader(artifactExport(t, [2]string{"outputs/a", "data"}))); err != nil {
		t.Fatal(err)
	}
	var applied int64
	if err := pool.QueryRow(t.Context(), `INSERT INTO turn_inputs(session_id, turn_id, idempotency_key, kind, payload)
		VALUES ($1, $2, 'message', 'message', '{}') RETURNING sequence`, session, turn).Scan(&applied); err != nil {
		t.Fatal(err)
	}
	if err := operations.BeginTurnArtifactCapture(t.Context(), tenant, session, turn, applied-1); !errors.Is(err, sessions.ErrUnappliedInputs) {
		t.Fatal("capture before the inputs applied", err)
	}
	if err := operations.BeginTurnArtifactCapture(t.Context(), tenant, session, turn, applied); err != nil {
		t.Fatal(err)
	}
	if err := operations.BeginTurnArtifactCapture(t.Context(), tenant, session, turn, applied); !errors.Is(err, sessions.ErrTurnConflict) {
		t.Fatal("second capture", err)
	}

	completedAt := time.Now().Add(-time.Hour).Truncate(time.Millisecond)
	outcome := json.RawMessage(fmt.Sprintf(`{"done":{"source_completed_at_ms":%d}}`, completedAt.UnixMilli()))
	_, before := journal(t, pool, pgunit.PathID(session))
	if _, err := operations.CompleteExecution(t.Context(), tenant, session, turn, sessions.TurnCompleted, outcome, "", applied-1); !errors.Is(err, sessions.ErrUnappliedInputs) {
		t.Fatal("completion before the inputs applied", err)
	}
	// The Session has no bound device to remember the native session on.
	if _, err := operations.CompleteExecution(t.Context(), tenant, session, turn, sessions.TurnCompleted, outcome, "native", applied); !errors.Is(err, sessions.ErrNotFound) {
		t.Fatal("completion without a bound device", err)
	}
	if entries, count, _ := recorded(t, pool, pgunit.PathID(turn)); len(entries) != 0 || count != 0 {
		t.Fatalf("rolled back completion kept %q", entries)
	}
	if _, after := journal(t, pool, pgunit.PathID(session)); len(after) != len(before) {
		t.Fatalf("rolled back completion journaled %q", kinds(after[len(before):]))
	}

	ended, err := operations.CompleteExecution(t.Context(), tenant, session, turn, sessions.TurnCompleted, outcome, "", applied)
	if err != nil || ended.Status != sessions.TurnCompleted || !ended.CompletedAt.Equal(completedAt) {
		t.Fatalf("completion: %+v, %v", ended, err)
	}
	if entries, _, _ := recorded(t, pool, pgunit.PathID(turn)); !reflect.DeepEqual(entries, []string{"execution_completed"}) {
		t.Fatalf("turn journal %q", entries)
	}
	var published int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM session_artifacts WHERE turn_id = $1 AND created_at IS NOT NULL`, turn).Scan(&published); err != nil || published != 1 {
		t.Fatalf("published %d Artifacts: %v", published, err)
	}
	_, after := journal(t, pool, pgunit.PathID(session))
	if got, want := kinds(after[len(before):]), []string{"agent.session.turn.completed", "agent.session.idle"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("session journal %q, want %q", got, want)
	}

	next := text(addTurn(t, pool, pgunit.PathID(session), sessions.TurnQueued))
	if err := lease.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := operations.TransitionTurn(t.Context(), tenant, session, next, sessions.TurnTransition{ExpectedStatus: sessions.TurnQueued, Status: sessions.TurnCancelled}); !errors.Is(err, pgunit.ErrLeaseClosed) {
		t.Fatal("closed lease moved a Turn", err)
	}
	if _, err := operations.CompleteExecution(t.Context(), tenant, session, next, sessions.TurnFailed, nil, "", 0); !errors.Is(err, pgunit.ErrLeaseClosed) {
		t.Fatal("closed lease completed a Turn", err)
	}
}

// Turn pages keep creation order with an ID tie-breaker in both directions,
// and a cursor or Session outside the tenant's Session is missing.
func TestTurnPagesKeepScopeAndOrder(t *testing.T) {
	pool := pgtest.Open(t)
	store := New(pgunit.NewPool(pool), pgtest.CredentialKey(t))
	ctx := t.Context()
	tenantID, sessionID, first := newTurn(t, pool, sessions.TurnCancelled)
	tenant, session := text(tenantID), text(sessionID)
	ids := []string{text(first)}
	for range 3 {
		ids = append(ids, text(addTurn(t, pool, sessionID, sessions.TurnCancelled)))
	}
	// Equal creation times exercise the ID tie-breaker across page boundaries.
	exec(t, pool, `UPDATE turns SET created_at = $1 WHERE session_id = $2`, time.Unix(1700000000, 0), sessionID)
	slices.Sort(ids)
	for _, ascending := range []bool{true, false} {
		cursor := ""
		for i := range ids {
			page, err := store.ListTurns(ctx, tenant, session, cursor, 1, ascending)
			at := i
			if !ascending {
				at = len(ids) - 1 - i
			}
			if err != nil || len(page.Turns) != 1 || page.Turns[0].ID != ids[at] {
				t.Fatalf("page %d: %+v %v", i, page, err)
			}
			if (page.NextCursor != "") != (i < len(ids)-1) {
				t.Fatalf("incorrect has_more: %+v", page)
			}
			cursor = page.Turns[0].ID
		}
		page, err := store.ListTurns(ctx, tenant, session, cursor, 1, ascending)
		if err != nil || len(page.Turns) != 0 || page.Turns == nil || page.NextCursor != "" {
			t.Fatalf("end: %+v %v", page, err)
		}
	}
	otherTenant, otherSession, _ := newTurn(t, pool, sessions.TurnCancelled)
	empty := uuid.New()
	exec(t, pool, `INSERT INTO sessions(id, tenant_id, engine, idempotency_key, request_hash) VALUES ($1, $2, 'codex', 'empty', 'hash')`, empty, tenantID)
	for _, scope := range [][2]string{{text(otherTenant), session}, {tenant, text(otherSession)}, {tenant, uuid.NewString()}, {tenant, empty.String()}} {
		if _, err := store.ListTurns(ctx, scope[0], scope[1], ids[0], 1, true); !errors.Is(err, sessions.ErrNotFound) {
			t.Fatalf("foreign cursor/session accepted: %v", err)
		}
	}
	page, err := store.ListTurns(ctx, tenant, empty.String(), "", 20, false)
	if err != nil || page.Turns == nil || len(page.Turns) != 0 {
		t.Fatalf("empty session: %+v %v", page, err)
	}
	for _, limit := range []int{0, 101} {
		if _, err := store.ListTurns(ctx, tenant, session, "", limit, false); !errors.Is(err, sessions.ErrInvalidInput) {
			t.Fatalf("limit accepted: %v", err)
		}
	}
}
