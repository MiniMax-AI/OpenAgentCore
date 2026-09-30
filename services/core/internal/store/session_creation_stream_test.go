package store

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
)

func TestCreationStreamStartsBeforeOwnInputsAndRetriesAtUpsertCursor(t *testing.T) {
	s, _ := testStore(t)
	other, _ := testStore(t)
	ctx := context.Background()
	tenant := uuid.NewString()
	input := CreateSessionInput{Creator: FixtureCreator(), Engine: "codex", IdempotencyKey: "stream", InitialInputs: []Input{messageInput("first")}}
	var wg sync.WaitGroup
	results := make(chan SessionCreation, 8)
	for i := range 8 {
		wg.Go(func() {
			st := s
			if i%2 == 0 {
				st = other
			}
			result, err := st.CreateSessionStream(ctx, tenant, input)
			if err != nil {
				t.Error(err)
				return
			}
			results <- result
		})
	}
	wg.Wait()
	close(results)
	var created SessionCreation
	var retries []SessionCreation
	for result := range results {
		if result.Created {
			if created.Created {
				t.Fatal("two creation owners")
			}
			created = result
		} else {
			retries = append(retries, result)
		}
	}
	// The snapshot is the committed post-admission projection; the cursor still
	// precedes the initial input's events.
	if !created.Created || created.Cursor != 0 || created.Session.LastTurn == nil || created.Session.LastTurn.Status != TurnQueued || len(retries) != 7 {
		t.Fatal("invalid post-admission creation snapshot", created, retries)
	}
	id := created.Session.ID
	initial, err := s.ListSessionEvents(ctx, tenant, id, created.Cursor)
	if err != nil || len(initial) != 3 {
		t.Fatal("lost initial events", initial, err)
	}
	for i, kind := range []string{"agent.session.turn.created", "agent.session.turn.item.added", "agent.session.in_progress"} {
		if initial[i].Event.Type != kind {
			t.Fatal("new Turn events are out of order", i, initial[i].Event.Type)
		}
	}
	if initial[1].Event.Item == nil || initial[1].Event.Item.Role != "user" || initial[2].Turn == nil || initial[2].Turn.Status != TurnQueued {
		t.Fatal("invalid initial input or activity snapshot", initial)
	}
	encoded := func(value Session) string {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	read, err := s.GetSession(ctx, tenant, id)
	if err != nil {
		t.Fatal(err)
	}
	for _, retry := range retries {
		if retry.Session.ID != id || retry.Cursor != initial[len(initial)-1].Sequence {
			t.Fatal(retry)
		}
		// Upsert retries return only the row, without a projection read or input.
		if retry.Session.LastTurn != nil || retry.Session.EnvironmentInputActivity != nil || retry.Session.Usage != nil {
			t.Fatal("retry read the Session projection", retry.Session)
		}
		if events, err := s.ListSessionEvents(ctx, tenant, id, retry.Cursor); err != nil || len(events) != 0 {
			t.Fatal("retry replayed initial events", events, err)
		}
	}
	ordinary, err := s.CreateSession(ctx, tenant, input)
	if err != nil || ordinary.ID != id || ordinary.LastTurn == nil {
		t.Fatal(ordinary, err)
	}
	// The streamed snapshot is the projection that the JSON response returns.
	if encoded(created.Session) != encoded(ordinary) || encoded(ordinary) != encoded(read) {
		t.Fatal("streamed and JSON creation projections differ", created.Session, ordinary)
	}
	snapshot, cursor, err := s.SessionStreamSnapshot(ctx, tenant, id)
	if err != nil || encoded(snapshot) != encoded(read) || cursor != initial[len(initial)-1].Sequence || initial[2].Settled {
		t.Fatal("stream snapshot differs from the Session read and its cursor", cursor, err)
	}
	transition(t, s, tenant, id, ordinary.LastTurn.ID, TurnQueued, TurnInProgress)
	transition(t, s, tenant, id, ordinary.LastTurn.ID, TurnInProgress, TurnCompleted)
	// Completing before the HTTP observer drains does not change its start point.
	all, err := s.ListSessionEvents(ctx, tenant, id, created.Cursor)
	if err != nil || len(all) <= len(initial) || all[0].Event.EventID != initial[0].Event.EventID {
		t.Fatal(all, err)
	}
	// The terminal Turn's idle snapshot is recorded as settled for creation streams.
	if last := all[len(all)-1]; last.Event.Type != "agent.session.idle" || !last.Settled {
		t.Fatal("terminal idle is not recorded as settled", last.Event.Type, last.Settled)
	}
	if _, cursor, err := s.SessionStreamSnapshot(ctx, tenant, id); err != nil || cursor != all[len(all)-1].Sequence {
		t.Fatal("stream snapshot cursor", cursor, err)
	}
	if _, _, err := s.SessionStreamSnapshot(ctx, uuid.NewString(), id); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign stream snapshot", err)
	}
	late, err := s.CreateSessionStream(ctx, tenant, input)
	if err != nil || late.Created || late.Cursor != all[len(all)-1].Sequence {
		t.Fatal(late, err)
	}
	next, err := s.SubmitInputs(ctx, tenant, id, "next", []Input{messageInput("later")})
	if err != nil {
		t.Fatal(err)
	}
	if late.Session.ID != id || late.Session.LastTurn != nil {
		t.Fatal("late retry read the Session projection", late.Session.LastTurn)
	}
	future, err := s.ListSessionEvents(ctx, tenant, id, late.Cursor)
	if err != nil || len(future) != 3 || future[0].Turn.ID != next[0].TurnID {
		t.Fatal(future, err)
	}
	for i, kind := range []string{"agent.session.turn.created", "agent.session.turn.item.added", "agent.session.in_progress"} {
		if future[i].Event.Type != kind {
			t.Fatal("later Turn events are out of order", i, future[i].Event.Type)
		}
	}
	turns, err := s.ListTurns(ctx, tenant, id, "", 100, true)
	if err != nil || len(turns.Turns) != 2 {
		t.Fatal(turns, err)
	}
	foreign, err := s.CreateSessionStream(ctx, uuid.NewString(), input)
	if err != nil || !foreign.Created || foreign.Session.ID == id || foreign.Cursor != 0 {
		t.Fatal(foreign, err)
	}
}

func TestCreationStreamIdleAndNonstreamRetry(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	tenant := uuid.NewString()
	input := CreateSessionInput{Creator: FixtureCreator(), Engine: "codex", IdempotencyKey: "idle"}
	first, err := s.CreateSessionStream(ctx, tenant, input)
	if err != nil || !first.Created || first.Cursor != 0 || first.Session.LastTurn != nil {
		t.Fatal(first, err)
	}
	if _, err := s.SubmitInputs(ctx, tenant, first.Session.ID, "message", []Input{messageInput("later")}); err != nil {
		t.Fatal(err)
	}
	retry, err := s.CreateSessionStream(ctx, tenant, input)
	if err != nil || retry.Created || retry.Cursor == 0 {
		t.Fatal(retry, err)
	}
	ordinary, err := s.CreateSession(ctx, tenant, input)
	if err != nil || ordinary.ID != first.Session.ID || ordinary.LastTurn == nil {
		t.Fatal(ordinary, err)
	}
	input.IdempotencyKey = "nonstream-first"
	ordinary, err = s.CreateSession(ctx, tenant, input)
	if err != nil {
		t.Fatal(err)
	}
	retry, err = s.CreateSessionStream(ctx, tenant, input)
	if err != nil || retry.Created || retry.Session.ID != ordinary.ID || retry.Cursor != 0 {
		t.Fatal(retry, err)
	}
}
