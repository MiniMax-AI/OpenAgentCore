package sessionpg

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgtest"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

var reservedBatch = []sessions.Input{messageInput("first"), messageInput("second")}

// environmentInputHistory checks the Session's Turns and admitted inputs, one
// Item per input, and that no input left Turn or Item events.
func environmentInputHistory(t *testing.T, pool *pgxpool.Pool, session pgtype.UUID, turns, inputs int) {
	t.Helper()
	var gotTurns, gotInputs, items, events int
	err := pool.QueryRow(t.Context(), `
		SELECT (SELECT count(*) FROM turns WHERE session_id=$1),
		       (SELECT count(*) FROM turn_inputs WHERE session_id=$1),
		       (SELECT count(*) FROM session_items WHERE session_id=$1),
		       (SELECT count(*) FROM session_events WHERE session_id=$1
		        AND (payload ? 'turn' OR payload->'event' ? 'item'))`, session).Scan(&gotTurns, &gotInputs, &items, &events)
	if err != nil || gotTurns != turns || gotInputs != inputs || items != inputs || (inputs == 0 && events != 0) {
		t.Fatal("history", gotTurns, gotInputs, items, events, err)
	}
}

// reserve reserves the two-message batch under key.
func reserve(t *testing.T, service *sessions.Service, tenant, session pgtype.UUID, key string) sessions.EnvironmentInputReservation {
	t.Helper()
	got, err := service.ReserveEnvironmentInput(t.Context(), text(tenant), text(session), key, reservedBatch)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// passDeadline moves the reservation's deadline into the past.
func passDeadline(t *testing.T, pool *pgxpool.Pool, reservation string) {
	t.Helper()
	exec(t, pool, `UPDATE environment_input_reservations SET deadline=clock_timestamp()-interval '1 second' WHERE id=$1`, reservation)
}

// cancelPending cancels the Session's pending input as Session cancellation
// does, then reads the reservation back.
func cancelPending(ctx context.Context, pool *pgxpool.Pool, store *Store, tenant, session pgtype.UUID, reservation string) (sessions.EnvironmentInputReservation, error) {
	err := WithSession(ctx, pgunit.NewPool(pool), tenant, session, func(ctx context.Context, q *sqlc.Queries, locked sessions.LockedSession) error {
		if err := locked.Public(); err != nil {
			return err
		}
		bound := BindSession(q, tenant, session)
		return sessions.TrackInputActivity(ctx, bound, bound.CancelPendingInput)
	})
	if err != nil {
		return sessions.EnvironmentInputReservation{}, err
	}
	return store.GetEnvironmentInputReservation(ctx, text(tenant), text(session), reservation)
}

// Concurrent equivalent reservations from two pools share one pending
// identity and deadline, which a restart keeps.
func TestEnvironmentInputReservationConcurrentIdentity(t *testing.T) {
	pool := pgtest.Open(t)
	_, service := stagingService(t, pool)
	_, other := stagingService(t, pgtest.Open(t))
	tenant, session, _ := newEnvironment(t, pool, "self_hosted", "pending")
	ctx := t.Context()
	batch := []sessions.Input{
		{Kind: "message", Payload: json.RawMessage(`{"text":"first","detail":{"a":1,"b":2}}`)},
		messageInput("second"),
	}
	const count = 8
	results := make(chan sessions.EnvironmentInputReservation, count)
	var wg sync.WaitGroup
	for i := range count {
		wg.Go(func() {
			admission := service
			inputs := append([]sessions.Input(nil), batch...)
			if i%2 == 0 {
				admission = other
				inputs[0].Payload = json.RawMessage(` { "detail": {"b": 2, "a": 1}, "text": "first" } `)
			}
			got, err := admission.ReserveEnvironmentInput(ctx, text(tenant), text(session), "request", inputs)
			if err != nil {
				t.Error(err)
				return
			}
			results <- got
		})
	}
	wg.Wait()
	close(results)
	var first sessions.EnvironmentInputReservation
	received := 0
	for result := range results {
		received++
		if first.ID == "" {
			first = result
		}
		if !reflect.DeepEqual(first, result) {
			t.Fatal("reservation identity changed", first, result)
		}
	}
	if received != count || first.State != sessions.EnvironmentInputPending || first.ID == "" || first.Deadline.Sub(first.CreatedAt) != 5*time.Minute || first.SettledAt != nil || len(first.Receipts) != 0 {
		t.Fatal("invalid pending result", received, first)
	}
	environmentInputHistory(t, pool, session, 0, 0)
	for _, changed := range [][]sessions.Input{batch[:1], {batch[1], batch[0]}, {messageInput("changed"), batch[1]}} {
		if _, err := service.ReserveEnvironmentInput(ctx, text(tenant), text(session), "request", changed); !errors.Is(err, sessions.ErrIdempotencyConflict) {
			t.Fatal("changed request accepted", err)
		}
	}
	if _, err := other.ReserveEnvironmentInput(ctx, text(tenant), text(session), "other", batch); !errors.Is(err, sessions.ErrTurnConflict) {
		t.Fatal("second pending request accepted", err)
	}
	pool.Close()
	_, restarted := stagingService(t, pgtest.Open(t))
	got, err := restarted.ReserveEnvironmentInput(ctx, text(tenant), text(session), "request", batch)
	if err != nil || !reflect.DeepEqual(first, got) {
		t.Fatal("restart changed deadline or identity", got, err)
	}
}

// A key that direct admission already used keeps its receipts and gains no
// reservation, and its retry leaves newer pending input alone.
func TestEnvironmentInputReservationKeepsEarlierDirectIdentity(t *testing.T) {
	pool := pgtest.Open(t)
	store, service := stagingService(t, pool)
	tenant, session, _ := newEnvironment(t, pool, "self_hosted", "pending")
	ctx := t.Context()
	input := messageInput("already admitted")
	receipts, err := service.SubmitInputs(ctx, text(tenant), text(session), "direct", []sessions.Input{input})
	if err != nil {
		t.Fatal(err)
	}
	move(t, pool, tenant, session, receipts[0].TurnID, sessions.TurnQueued, sessions.TurnInProgress)
	move(t, pool, tenant, session, receipts[0].TurnID, sessions.TurnInProgress, sessions.TurnCompleted)
	pending := reserve(t, service, tenant, session, "new")
	got, err := service.ReserveEnvironmentInput(ctx, text(tenant), text(session), "direct", []sessions.Input{input})
	if err != nil || got.State != sessions.EnvironmentInputAdmitted || got.ID != "" || !got.Deadline.IsZero() || len(got.Receipts) != 1 || got.Receipts[0].Sequence != receipts[0].Sequence {
		t.Fatal("direct admission gained a reservation", got, err)
	}
	if _, err := service.ReserveEnvironmentInput(ctx, text(tenant), text(session), "direct", []sessions.Input{messageInput("changed")}); !errors.Is(err, sessions.ErrIdempotencyConflict) {
		t.Fatal(err)
	}
	retry, err := service.SubmitInputs(ctx, text(tenant), text(session), "direct", []sessions.Input{input})
	if err != nil || len(retry) != 1 || !retry[0].Replayed {
		t.Fatal(retry, err)
	}
	retained, err := store.GetEnvironmentInputReservation(ctx, text(tenant), text(session), pending.ID)
	if err != nil || !reflect.DeepEqual(retained, pending) {
		t.Fatal("old retry affected new pending input", retained, err)
	}
}
