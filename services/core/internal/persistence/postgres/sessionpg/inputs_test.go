package sessionpg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgtest"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

var messagePayload = json.RawMessage(`{"input":[{"role":"user","content":[{"type":"input_text","text":"hello"}]}]}`)

var cancelInput = sessions.Input{Kind: "cancel", Payload: json.RawMessage(`{}`)}

func messageInput(text string) sessions.Input {
	payload, _ := json.Marshal(map[string]string{"text": text})
	return sessions.Input{Kind: "message", Payload: payload}
}

// newSession stores a fresh tenant's idle Session without an Environment and
// returns their IDs.
func newSession(t *testing.T, pool *pgxpool.Pool) (pgtype.UUID, pgtype.UUID) {
	t.Helper()
	tenant, session := uuid.New(), uuid.New()
	exec(t, pool, `INSERT INTO sessions(id, tenant_id, engine, idempotency_key, request_hash) VALUES ($1, $2, 'codex', 'key', 'hash')`, session, tenant)
	return pgID(tenant), pgID(session)
}

// submit admits one input through the Session service.
func submit(ctx context.Context, service *sessions.Service, tenant, session pgtype.UUID, key string, input sessions.Input) (sessions.InputReceipt, error) {
	receipts, err := service.SubmitInputs(ctx, text(tenant), text(session), key, []sessions.Input{input})
	if err != nil {
		return sessions.InputReceipt{}, err
	}
	return receipts[0], nil
}

func submitMessage(t *testing.T, service *sessions.Service, tenant, session pgtype.UUID, key string) sessions.InputReceipt {
	t.Helper()
	receipt, err := submit(t.Context(), service, tenant, session, key, sessions.Input{Kind: "message", Payload: messagePayload})
	if err != nil {
		t.Fatal(err)
	}
	return receipt
}

func move(t *testing.T, pool *pgxpool.Pool, tenant, session pgtype.UUID, turn, from, to string) sessions.Turn {
	t.Helper()
	moved, err := transition(t.Context(), pool, tenant, session, turn, sessions.TurnTransition{ExpectedStatus: from, Status: to})
	if err != nil {
		t.Fatal(err)
	}
	return moved
}

// Concurrent messages from two pools all join one Turn, and concurrent retries
// of one key commit one input.
func TestConcurrentInputsUseOneTurnAndOneRetryReceipt(t *testing.T) {
	pool := pgtest.Open(t)
	store, service := stagingService(t, pool)
	_, other := stagingService(t, pgtest.Open(t))
	tenant, session := newSession(t, pool)
	const count = 8
	for _, repeatedKey := range []bool{true, false} {
		t.Run(fmt.Sprintf("repeated-key-%v", repeatedKey), func(t *testing.T) {
			var wg sync.WaitGroup
			receipts := make(chan sessions.InputReceipt, count)
			errs := make(chan error, count)
			for i := range count {
				wg.Go(func() {
					key := "same"
					if !repeatedKey {
						key = fmt.Sprintf("distinct-%d", i)
					}
					admission := service
					if i%2 == 0 {
						admission = other
					}
					r, err := submit(t.Context(), admission, tenant, session, key, sessions.Input{Kind: "message", Payload: messagePayload})
					receipts <- r
					errs <- err
				})
			}
			wg.Wait()
			close(receipts)
			close(errs)
			for err := range errs {
				if err != nil {
					t.Fatal(err)
				}
			}
			turns, sequences := map[string]bool{}, map[int64]bool{}
			newReceipts := 0
			for r := range receipts {
				turns[r.TurnID], sequences[r.Sequence] = true, true
				if !r.Replayed {
					newReceipts++
				}
			}
			want := count
			if repeatedKey {
				want = 1
			}
			if len(turns) != 1 || turns[""] || len(sequences) != want || newReceipts != want {
				t.Fatalf("turns=%v sequences=%v new=%d", turns, sequences, newReceipts)
			}
		})
	}
	first := submitMessage(t, service, tenant, session, "same")
	inputs, err := store.ListTurnInputs(t.Context(), text(tenant), text(session), first.TurnID, 0, 100)
	if err != nil || len(inputs) != count+1 {
		t.Fatalf("lost or duplicated inputs: %d, %v", len(inputs), err)
	}
}

// An active Turn takes steering input, retries replay their receipts across a
// restart, an idle Session starts a new Turn, and admission changes no stored
// Session field besides its event sequence.
func TestTurnInputRetriesAndRestart(t *testing.T) {
	pool := pgtest.Open(t)
	_, service := stagingService(t, pool)
	tenant, session := newSession(t, pool)
	ctx := t.Context()
	stored := func(pool *pgxpool.Pool) (row string) {
		if err := pool.QueryRow(ctx, `SELECT (to_jsonb(s) - 'event_sequence')::text FROM sessions s WHERE id = $1`, session).Scan(&row); err != nil {
			t.Fatal(err)
		}
		return row
	}
	created := stored(pool)
	first := submitMessage(t, service, tenant, session, "first")
	move(t, pool, tenant, session, first.TurnID, sessions.TurnQueued, sessions.TurnInProgress)
	steer := submitMessage(t, service, tenant, session, "steer")
	if steer.TurnID != first.TurnID || steer.Sequence <= first.Sequence {
		t.Fatalf("active message did not steer: %+v", steer)
	}
	reordered := json.RawMessage(`{ "input": [{"content":[{"text":"hello","type":"input_text"}],"role":"user"}] }`)
	retry, err := submit(ctx, service, tenant, session, "first", sessions.Input{Kind: "message", Payload: reordered})
	if err != nil || !retry.Replayed || retry.Sequence != first.Sequence || retry.TurnID != first.TurnID {
		t.Fatalf("equivalent retry = %+v, %v", retry, err)
	}
	if _, err := submit(ctx, service, tenant, session, "first", messageInput("changed")); !errors.Is(err, sessions.ErrIdempotencyConflict) {
		t.Fatalf("changed payload accepted: %v", err)
	}
	if _, err := submit(ctx, service, tenant, session, "first", cancelInput); !errors.Is(err, sessions.ErrIdempotencyConflict) {
		t.Fatalf("changed input kind accepted: %v", err)
	}
	completed := move(t, pool, tenant, session, first.TurnID, sessions.TurnInProgress, sessions.TurnCompleted)
	next := submitMessage(t, service, tenant, session, "next")
	if next.TurnID == first.TurnID {
		t.Fatal("idle message did not start a new Turn")
	}
	pool.Close()
	reopened := pgtest.Open(t)
	recovered, restarted := stagingService(t, reopened)
	retry = submitMessage(t, restarted, tenant, session, "first")
	if !retry.Replayed || retry.TurnID != first.TurnID || retry.Sequence != first.Sequence {
		t.Fatalf("restart retry changed target: %+v", retry)
	}
	got, err := recovered.GetTurn(ctx, text(tenant), text(session), first.TurnID)
	if err != nil || !reflect.DeepEqual(got, completed) {
		t.Fatalf("restart turn: %+v, %v", got, err)
	}
	var all []sessions.TurnInput
	var cursor int64
	for {
		page, err := recovered.ListTurnInputs(ctx, text(tenant), text(session), first.TurnID, cursor, 1)
		if err != nil {
			t.Fatal(err)
		}
		if len(page) == 0 {
			break
		}
		if page[0].Sequence <= cursor || page[0].CreatedAt.IsZero() {
			t.Fatalf("invalid ordered input: %+v", page)
		}
		cursor = page[0].Sequence
		all = append(all, page...)
	}
	if len(all) != 2 || all[0].Sequence != first.Sequence || all[1].Sequence != steer.Sequence {
		t.Fatalf("recovered inputs = %+v", all)
	}
	snapshot, err := recovered.GetSession(ctx, text(tenant), text(session))
	if err != nil || snapshot.LastTurn == nil || snapshot.LastTurn.ID != next.TurnID || snapshot.LastTurn.Status != sessions.TurnQueued {
		t.Fatal("latest Session activity did not survive restart", err)
	}
	if stored(reopened) != created {
		t.Fatal("input admission mutated the stored Session")
	}
}

// Input admission and Turn reads and writes resolve only the tenant's own
// Session, and a Turn only within its Session.
func TestTurnOperationsAreTenantAndSessionScoped(t *testing.T) {
	pool := pgtest.Open(t)
	store, service := stagingService(t, pool)
	tenant, session := newSession(t, pool)
	otherTenant, other := newSession(t, pool)
	ctx := t.Context()
	first := submitMessage(t, service, tenant, session, "input")
	for _, scope := range []struct{ tenant, session pgtype.UUID }{{otherTenant, session}, {tenant, other}, {tenant, pgID(uuid.New())}} {
		for name, call := range map[string]func() error{
			"submit": func() error {
				_, err := submit(ctx, service, scope.tenant, scope.session, "input", sessions.Input{Kind: "message", Payload: messagePayload})
				return err
			},
			"cancel": func() error {
				_, err := submit(ctx, service, scope.tenant, scope.session, "cancel", cancelInput)
				return err
			},
			"read": func() error {
				_, err := store.GetTurn(ctx, text(scope.tenant), text(scope.session), first.TurnID)
				return err
			},
			"inputs": func() error {
				_, err := store.ListTurnInputs(ctx, text(scope.tenant), text(scope.session), first.TurnID, 0, 10)
				return err
			},
			"transition": func() error {
				_, err := transition(ctx, pool, scope.tenant, scope.session, first.TurnID, sessions.TurnTransition{ExpectedStatus: sessions.TurnQueued, Status: sessions.TurnFailed})
				return err
			},
		} {
			if err := call(); !errors.Is(err, sessions.ErrNotFound) {
				t.Fatalf("%s escaped scope: %v", name, err)
			}
		}
	}
	// Turn IDs cannot be used with another valid Session in the same tenant either.
	second := pgID(uuid.New())
	exec(t, pool, `INSERT INTO sessions(id, tenant_id, engine, idempotency_key, request_hash) VALUES ($1, $2, 'codex', 'second', 'hash')`, second, tenant)
	if _, err := store.GetTurn(ctx, text(tenant), text(second), first.TurnID); !errors.Is(err, sessions.ErrNotFound) {
		t.Fatalf("cross-session turn read: %v", err)
	}
	if _, err := transition(ctx, pool, tenant, second, first.TurnID, sessions.TurnTransition{ExpectedStatus: sessions.TurnQueued, Status: sessions.TurnFailed}); !errors.Is(err, sessions.ErrNotFound) {
		t.Fatalf("cross-session turn write: %v", err)
	}
	otherInput := submitMessage(t, service, otherTenant, other, "input")
	if otherInput.TurnID == first.TurnID {
		t.Fatal("retry identity leaked across Sessions")
	}
}

// Batches from two pools keep their inputs adjacent and in order, and one
// key's concurrent retries commit one batch.
func TestInputBatchesAreOrderedAndIdempotentAcrossConnections(t *testing.T) {
	pool := pgtest.Open(t)
	store, service := stagingService(t, pool)
	_, other := stagingService(t, pgtest.Open(t))
	tenant, session := newSession(t, pool)
	ctx := t.Context()
	const count = 8
	batch := []sessions.Input{messageInput("first"), messageInput("second")}
	for _, repeated := range []bool{true, false} {
		var wg sync.WaitGroup
		receipts := make(chan []sessions.InputReceipt, count)
		errs := make(chan error, count)
		for i := range count {
			wg.Go(func() {
				admission := service
				if i%2 == 0 {
					admission = other
				}
				key := "same-batch"
				if !repeated {
					key = fmt.Sprintf("batch-%d", i)
				}
				got, err := admission.SubmitInputs(ctx, text(tenant), text(session), key, batch)
				receipts <- got
				errs <- err
			})
		}
		wg.Wait()
		close(receipts)
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
		newBatches := 0
		for got := range receipts {
			if len(got) != 2 || got[0].TurnID == "" || got[0].TurnID != got[1].TurnID || got[0].Sequence >= got[1].Sequence || got[0].Replayed != got[1].Replayed {
				t.Fatalf("invalid batch receipt: %+v", got)
			}
			if !got[0].Replayed {
				newBatches++
			}
		}
		want := count
		if repeated {
			want = 1
		}
		if newBatches != want {
			t.Fatalf("accepted %d batches, want %d", newBatches, want)
		}
	}
	got, err := service.SubmitInputs(ctx, text(tenant), text(session), "same-batch", batch)
	if err != nil {
		t.Fatal(err)
	}
	inputs, err := store.ListTurnInputs(ctx, text(tenant), text(session), got[0].TurnID, 0, 100)
	if err != nil || len(inputs) != 2*(count+1) {
		t.Fatalf("inputs=%d err=%v", len(inputs), err)
	}
	for i, input := range inputs {
		var payload map[string]string
		if err := json.Unmarshal(input.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		want := "first"
		if i%2 == 1 {
			want = "second"
		}
		if payload["text"] != want {
			t.Fatalf("batch interleaved at %d: %v", i, payload)
		}
	}
}

// A batch retry compares the whole normalized request and replays the
// cancellation targets it committed, after a restart too.
func TestBatchRetriesCompareTheWholeRequestAndRetainTargets(t *testing.T) {
	pool := pgtest.Open(t)
	store, service := stagingService(t, pool)
	tenant, session := newSession(t, pool)
	ctx := t.Context()
	batch := []sessions.Input{cancelInput, messageInput("one"), cancelInput, messageInput("two")}
	first, err := service.SubmitInputs(ctx, text(tenant), text(session), "mixed", batch)
	if err != nil {
		t.Fatal(err)
	}
	if first[0].TurnID != "" || first[1].TurnID == "" || first[1].TurnID != first[2].TurnID || first[1].TurnID == first[3].TurnID {
		t.Fatalf("cancellation targets: %+v", first)
	}
	cancelled, err := store.GetTurn(ctx, text(tenant), text(session), first[1].TurnID)
	if err != nil || cancelled.Status != sessions.TurnCancelled {
		t.Fatalf("cancelled turn=%+v err=%v", cancelled, err)
	}
	move(t, pool, tenant, session, first[3].TurnID, sessions.TurnQueued, sessions.TurnInProgress)
	move(t, pool, tenant, session, first[3].TurnID, sessions.TurnInProgress, sessions.TurnCompleted)
	next := submitMessage(t, service, tenant, session, "next")
	for _, changed := range [][]sessions.Input{batch[:3], append(append([]sessions.Input{}, batch...), cancelInput), {batch[0], batch[3], batch[2], batch[1]}} {
		if _, err := service.SubmitInputs(ctx, text(tenant), text(session), "mixed", changed); !errors.Is(err, sessions.ErrIdempotencyConflict) {
			t.Fatalf("changed batch accepted: %v", err)
		}
	}
	batch[1].Payload = json.RawMessage(`{ "text" : "one" }`)
	pool.Close()
	restartedStore, restarted := stagingService(t, pgtest.Open(t))
	retry, err := restarted.SubmitInputs(ctx, text(tenant), text(session), "mixed", batch)
	for i := range first {
		first[i].Replayed = true
	}
	if err != nil || !reflect.DeepEqual(retry, first) {
		t.Fatalf("restart changed receipts: %+v, %v", retry, err)
	}
	current, err := restartedStore.GetTurn(ctx, text(tenant), text(session), next.TurnID)
	if err != nil || current.Status != sessions.TurnQueued || !current.CancelRequestedAt.IsZero() {
		t.Fatalf("retry cancelled later work: %+v, %v", current, err)
	}
	if _, err := restarted.SubmitInputs(ctx, uuid.NewString(), text(session), "mixed", batch); !errors.Is(err, sessions.ErrNotFound) {
		t.Fatalf("batch retry escaped tenant: %v", err)
	}
}

// A batch that fails part way rolls back its earlier cancellation and inputs,
// and its retry then commits it whole.
func TestFailedBatchRollsBackEarlierCancellationAndInputs(t *testing.T) {
	pool := pgtest.Open(t)
	store, service := stagingService(t, pool)
	tenant, session := newSession(t, pool)
	ctx := t.Context()
	initial := submitMessage(t, service, tenant, session, "initial")
	move(t, pool, tenant, session, initial.TurnID, sessions.TurnQueued, sessions.TurnInProgress)
	key := uuid.NewString()
	constraint := "batch_failure_" + strings.ReplaceAll(key, "-", "")
	// Inject a storage error on the second insert, after cancellation has run.
	exec(t, pool, "ALTER TABLE turn_inputs ADD CONSTRAINT "+constraint+" CHECK (idempotency_key <> '"+key+"' OR batch_position = 0)")
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "ALTER TABLE turn_inputs DROP CONSTRAINT IF EXISTS "+constraint)
	})
	batch := []sessions.Input{cancelInput, messageInput("after cancel")}
	if got, err := service.SubmitInputs(ctx, text(tenant), text(session), key, batch); err == nil || got != nil {
		t.Fatalf("partial batch succeeded: %+v %v", got, err)
	}
	turn, err := store.GetTurn(ctx, text(tenant), text(session), initial.TurnID)
	if err != nil || turn.Status != sessions.TurnInProgress || !turn.CancelRequestedAt.IsZero() {
		t.Fatalf("cancellation escaped rollback: %+v %v", turn, err)
	}
	inputs, err := store.ListTurnInputs(ctx, text(tenant), text(session), initial.TurnID, 0, 100)
	if err != nil || len(inputs) != 1 {
		t.Fatalf("partial inputs survived: %v %v", inputs, err)
	}
	exec(t, pool, "ALTER TABLE turn_inputs DROP CONSTRAINT "+constraint)
	got, err := service.SubmitInputs(ctx, text(tenant), text(session), key, batch)
	if err != nil || len(got) != 2 || got[0].Replayed || got[1].Replayed {
		t.Fatalf("retry after rollback: %+v %v", got, err)
	}
}

// A cancel request targets the Turn active when it was admitted, and its retry
// never retargets a later Turn; cancelling queued work stops it at once.
func TestCancellationStaysBoundToItsOriginalTurn(t *testing.T) {
	pool := pgtest.Open(t)
	store, service := stagingService(t, pool)
	tenant, session := newSession(t, pool)
	ctx := t.Context()
	idle, err := submit(ctx, service, tenant, session, "idle-cancel", cancelInput)
	if err != nil || idle.TurnID != "" || idle.Sequence == 0 {
		t.Fatalf("idle cancellation: %+v, %v", idle, err)
	}
	first := submitMessage(t, service, tenant, session, "first")
	started := move(t, pool, tenant, session, first.TurnID, sessions.TurnQueued, sessions.TurnInProgress)
	if started.StartedAt.IsZero() || !started.CompletedAt.IsZero() {
		t.Fatalf("started timestamps: %+v", started)
	}
	cancel, err := submit(ctx, service, tenant, session, "cancel", cancelInput)
	if err != nil || cancel.TurnID != first.TurnID {
		t.Fatalf("cancellation target: %+v, %v", cancel, err)
	}
	pending, err := store.GetTurn(ctx, text(tenant), text(session), first.TurnID)
	if err != nil || pending.Status != sessions.TurnInProgress || pending.CancelRequestedAt.IsZero() || !pending.CompletedAt.IsZero() {
		t.Fatalf("running cancellation falsely completed: %+v, %v", pending, err)
	}
	move(t, pool, tenant, session, first.TurnID, sessions.TurnInProgress, sessions.TurnCancelled)
	next := submitMessage(t, service, tenant, session, "next")
	for key, original := range map[string]sessions.InputReceipt{"cancel": cancel, "idle-cancel": idle} {
		retry, err := submit(ctx, service, tenant, session, key, cancelInput)
		if err != nil || !retry.Replayed || retry.TurnID != original.TurnID || retry.Sequence != original.Sequence {
			t.Fatalf("cancellation retargeted: %+v, %v", retry, err)
		}
	}
	queued, err := store.GetTurn(ctx, text(tenant), text(session), next.TurnID)
	if err != nil || queued.Status != sessions.TurnQueued || !queued.CancelRequestedAt.IsZero() {
		t.Fatalf("old cancellation affected later Turn: %+v, %v", queued, err)
	}
	if _, err := submit(ctx, service, tenant, session, "cancel-queued", cancelInput); err != nil {
		t.Fatal(err)
	}
	stopped, err := store.GetTurn(ctx, text(tenant), text(session), next.TurnID)
	if err != nil || stopped.Status != sessions.TurnCancelled || stopped.CompletedAt.IsZero() || !stopped.StartedAt.IsZero() {
		t.Fatalf("queued work did not stop: %+v, %v", stopped, err)
	}
	if _, err := transition(ctx, pool, tenant, session, next.TurnID, sessions.TurnTransition{ExpectedStatus: sessions.TurnQueued, Status: sessions.TurnInProgress}); !errors.Is(err, sessions.ErrTurnConflict) {
		t.Fatalf("cancelled queued work was started: %v", err)
	}
}

// A waiting Turn takes steering input and keeps its start time when it
// resumes; a cancel request keeps it from resuming.
func TestWaitingTurnRetainsInputsAndStartTime(t *testing.T) {
	pool := pgtest.Open(t)
	_, service := stagingService(t, pool)
	tenant, session := newSession(t, pool)
	first := submitMessage(t, service, tenant, session, "first")
	started := move(t, pool, tenant, session, first.TurnID, sessions.TurnQueued, sessions.TurnInProgress)
	move(t, pool, tenant, session, first.TurnID, sessions.TurnInProgress, sessions.TurnWaiting)
	steer := submitMessage(t, service, tenant, session, "steer")
	if steer.TurnID != first.TurnID {
		t.Fatal("waiting input started another Turn")
	}
	resumed := move(t, pool, tenant, session, first.TurnID, sessions.TurnWaiting, sessions.TurnInProgress)
	if !started.StartedAt.Equal(resumed.StartedAt) {
		t.Fatal("resume reset the start time")
	}
	move(t, pool, tenant, session, first.TurnID, sessions.TurnInProgress, sessions.TurnWaiting)
	if _, err := submit(t.Context(), service, tenant, session, "cancel", cancelInput); err != nil {
		t.Fatal(err)
	}
	if _, err := transition(t.Context(), pool, tenant, session, first.TurnID, sessions.TurnTransition{ExpectedStatus: sessions.TurnWaiting, Status: sessions.TurnInProgress}); !errors.Is(err, sessions.ErrTurnConflict) {
		t.Fatalf("cancelling Turn resumed: %v", err)
	}
	move(t, pool, tenant, session, first.TurnID, sessions.TurnWaiting, sessions.TurnCancelled)
}

// Rejected input and rejected transitions persist nothing.
func TestTurnInputValidationHasNoSideEffects(t *testing.T) {
	pool := pgtest.Open(t)
	store, service := stagingService(t, pool)
	tenant, session := newSession(t, pool)
	ctx := t.Context()
	for _, raw := range []json.RawMessage{nil, json.RawMessage(`[]`), json.RawMessage(`null`), json.RawMessage(`{} {}`), json.RawMessage(`{"text":"` + string(make([]byte, 512*1024)) + `"}`)} {
		if _, err := submit(ctx, service, tenant, session, "first", sessions.Input{Kind: "message", Payload: raw}); !errors.Is(err, sessions.ErrInvalidInput) {
			t.Fatalf("invalid input accepted: %v", err)
		}
	}
	cancelled, stop := context.WithCancel(ctx)
	stop()
	if _, err := submit(cancelled, service, tenant, session, "first", sessions.Input{Kind: "message", Payload: messagePayload}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled context: %v", err)
	}
	first := submitMessage(t, service, tenant, session, "first")
	if first.Replayed {
		t.Fatal("failed submission persisted a receipt")
	}
	for _, input := range []sessions.TurnTransition{
		{ExpectedStatus: sessions.TurnQueued, Status: sessions.TurnCompleted},
		{ExpectedStatus: sessions.TurnInProgress, Status: sessions.TurnQueued},
		{ExpectedStatus: sessions.TurnCompleted, Status: sessions.TurnInProgress},
		{ExpectedStatus: sessions.TurnQueued, Status: sessions.TurnInProgress, Outcome: json.RawMessage(`{"premature":true}`)},
	} {
		if _, err := transition(ctx, pool, tenant, session, first.TurnID, input); !errors.Is(err, sessions.ErrInvalidInput) {
			t.Fatalf("invalid transition accepted: %v", err)
		}
	}
	got, err := store.GetTurn(ctx, text(tenant), text(session), first.TurnID)
	if err != nil || got.Status != sessions.TurnQueued || !got.StartedAt.IsZero() {
		t.Fatalf("invalid transition changed Turn: %+v, %v", got, err)
	}
}
