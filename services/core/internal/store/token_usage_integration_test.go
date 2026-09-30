package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"testing"
)

func TestTokenUsageDurableSnapshotsAndSessionTotals(t *testing.T) {
	ctx := context.Background()
	s, pool := store.NewTestStore(t)
	tenant := uuid.NewString()
	session, err := s.CreateSession(ctx, tenant, store.CreateSessionInput{Creator: store.FixtureCreator(), Engine: "codex", IdempotencyKey: "usage"})
	if err != nil {
		t.Fatal(err)
	}
	usage := func(input int) json.RawMessage {
		return json.RawMessage(fmt.Sprintf(`{"tokens":{"input_tokens":%d,"cached_input_tokens":4,"output_tokens":3,"reasoning_output_tokens":2,"total_tokens":%d}}`, input, input+3))
	}
	check := func(raw json.RawMessage, input int) {
		t.Helper()
		var got v1.TokenUsage
		if json.Unmarshal(raw, &got) != nil || got.InputTokens != int64(input) || got.TotalTokens != int64(input+3) || got.InputTokensDetails.CachedTokens != 4 || got.OutputTokensDetails.ReasoningTokens != 2 {
			t.Fatalf("unexpected usage: %s", raw)
		}
	}
	for n, status := range []string{sessions.TurnFailed, sessions.TurnCancelled} {
		admission, err := s.SubmitMessage(ctx, tenant, session.ID, fmt.Sprint(n), json.RawMessage(`{"text":"measure"}`))
		if err != nil {
			t.Fatal(err)
		}
		_, err = s.TransitionTurn(ctx, tenant, session.ID, admission.TurnID, store.TurnTransition{ExpectedStatus: sessions.TurnQueued, Status: sessions.TurnInProgress})
		if err != nil {
			t.Fatal(err)
		}
		batch := []store.ExecutionEvent{{Kind: "usage", Payload: usage(10)}}
		for range 2 {
			if err = s.AppendTurnEvents(ctx, tenant, session.ID, admission.TurnID, 1, batch); err != nil {
				t.Fatal(err)
			}
		}
		// A later snapshot replaces the earlier measurement; it is not a delta.
		if err = s.AppendTurnEvents(ctx, tenant, session.ID, admission.TurnID, 2, []store.ExecutionEvent{{Kind: "usage", Payload: usage(20)}}); err != nil {
			t.Fatal(err)
		}
		measured, err := s.GetTurn(ctx, tenant, session.ID, admission.TurnID)
		if err != nil {
			t.Fatal(err)
		}
		check(measured.Usage, 20)
		if _, err = s.CompleteExecution(ctx, tenant, session.ID, admission.TurnID, sessions.TurnCompleted, json.RawMessage(`{"done":{"usage":`+string(usage(99))+`}}`), "missing-binding", admission.Sequence); !errors.Is(err, store.ErrNotFound) {
			t.Fatal(err)
		}
		rolledBack, err := s.GetTurn(ctx, tenant, session.ID, admission.TurnID)
		if err != nil || rolledBack.Status != sessions.TurnInProgress {
			t.Fatalf("rollback: %+v %v", rolledBack, err)
		}
		check(rolledBack.Usage, 20)
		// Completion without usage retains the last persisted measurement.
		completed, err := s.CompleteExecution(ctx, tenant, session.ID, admission.TurnID, status, json.RawMessage(`{"done":{"content":"partial"}}`), "", admission.Sequence)
		if err != nil {
			t.Fatal(err)
		}
		check(completed.Usage, 20)
		if _, err = s.CompleteExecution(ctx, tenant, session.ID, admission.TurnID, status, json.RawMessage(`{"done":{"usage":`+string(usage(99))+`}}`), "", admission.Sequence); !errors.Is(err, store.ErrTurnConflict) {
			t.Fatal(err)
		}
		if _, err = s.GetTurn(ctx, uuid.NewString(), session.ID, admission.TurnID); !errors.Is(err, store.ErrNotFound) {
			t.Fatal(err)
		}
	}
	// A separate connection pool must recover the committed totals without engine state.
	restored, err := pgxpool.NewWithConfig(ctx, pool.Config())
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	fresh := store.New(restored)
	got, err := fresh.GetSession(ctx, tenant, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	var total v1.TokenUsage
	if err = json.Unmarshal(got.Usage, &total); err != nil {
		t.Fatal(err)
	}
	if total.InputTokens != 40 || total.OutputTokens != 6 || total.TotalTokens != 46 || total.InputTokensDetails.CachedTokens != 8 || total.OutputTokensDetails.ReasoningTokens != 4 {
		t.Fatalf("double counted totals: %+v", total)
	}
	page, err := fresh.ListSessions(ctx, tenant, "", 100, true, nil)
	if err != nil || len(page.Sessions) != 1 || string(page.Sessions[0].Usage) != string(got.Usage) {
		t.Fatalf("list totals: %+v %v", page, err)
	}
	if _, err = fresh.GetSession(ctx, uuid.NewString(), session.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatal(err)
	}
}

func TestCancellationReceiptUsageSurvivesRecovery(t *testing.T) {
	ctx := context.Background()
	s, _ := store.NewTestStore(t)
	tenant := uuid.NewString()
	session, err := s.CreateSession(ctx, tenant, store.CreateSessionInput{Creator: store.FixtureCreator(), Engine: "codex", IdempotencyKey: "cancel-recovery"})
	if err != nil {
		t.Fatal(err)
	}
	admission, err := s.SubmitMessage(ctx, tenant, session.ID, "start", json.RawMessage(`{"text":"measure"}`))
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.TransitionTurn(ctx, tenant, session.ID, admission.TurnID, store.TurnTransition{ExpectedStatus: sessions.TurnQueued, Status: sessions.TurnInProgress})
	if err != nil {
		t.Fatal(err)
	}
	receipt := json.RawMessage(`{"applied":true,"outcome":{"usage":{"tokens":{"input_tokens":10,"cached_input_tokens":4,"output_tokens":3,"reasoning_output_tokens":2,"total_tokens":13}}}}`)
	if err = s.AppendTurnEvents(ctx, tenant, session.ID, admission.TurnID, 1, []store.ExecutionEvent{{Kind: "cancel_receipt", Payload: receipt}}); err != nil {
		t.Fatal(err)
	}
	// Startup recovery has no in-memory cancellation outcome.
	recovered, err := s.TransitionTurn(ctx, tenant, session.ID, admission.TurnID, store.TurnTransition{ExpectedStatus: sessions.TurnInProgress, Status: sessions.TurnFailed, Outcome: json.RawMessage(`{"error_code":"execution_interrupted"}`)})
	if err != nil {
		t.Fatal(err)
	}
	var got v1.TokenUsage
	if json.Unmarshal(recovered.Usage, &got) != nil || got.TotalTokens != 13 {
		t.Fatalf("recovery lost receipt usage: %s", recovered.Usage)
	}
}

// Official Session usage is the sum only when every root Turn has ended with
// known usage: it stays null while a Turn is queued, active or waiting (ST-03)
// and after a Turn ends with unknown usage (EVT-13).
func TestSessionUsageRequiresEveryRootTurnEndedAndMeasured(t *testing.T) {
	ctx := context.Background()
	s, _ := store.NewTestStore(t)
	tenant := uuid.NewString()
	session, err := s.CreateSession(ctx, tenant, store.CreateSessionInput{Creator: store.FixtureCreator(), Engine: "codex", IdempotencyKey: "unknown-usage"})
	if err != nil {
		t.Fatal(err)
	}
	usage := func(input int) []store.ExecutionEvent {
		return []store.ExecutionEvent{{Kind: "usage", Payload: json.RawMessage(fmt.Sprintf(`{"tokens":{"input_tokens":%d,"cached_input_tokens":4,"output_tokens":3,"reasoning_output_tokens":2,"total_tokens":%d}}`, input, input+3))}}
	}
	// Runtime telemetry keeps counting every recorded snapshot, active Turns
	// included, and is scoped to the tenant.
	measured := func(want int64) {
		t.Helper()
		got, err := s.MeasuredSessionUsage(ctx, tenant, session.ID)
		var value v1.TokenUsage
		if err != nil || (want < 0) != (got == nil) || (want >= 0 && (json.Unmarshal(got, &value) != nil || value.TotalTokens != want)) {
			t.Fatalf("measured usage = %s %v, want total %d", got, err, want)
		}
		if foreign, err := s.MeasuredSessionUsage(ctx, uuid.NewString(), session.ID); err != nil || foreign != nil {
			t.Fatalf("foreign measured usage: %s %v", foreign, err)
		}
	}
	total := func(want int64) {
		t.Helper()
		got, err := s.GetSession(ctx, tenant, session.ID)
		if err != nil {
			t.Fatal(err)
		}
		page, err := s.ListSessions(ctx, tenant, "", 100, true, nil)
		if err != nil || len(page.Sessions) != 1 || string(page.Sessions[0].Usage) != string(got.Usage) {
			t.Fatalf("list usage: %+v %v", page, err)
		}
		if want < 0 {
			if got.Usage != nil {
				t.Fatalf("usage = %s, want null", got.Usage)
			}
			return
		}
		var value v1.TokenUsage
		if json.Unmarshal(got.Usage, &value) != nil || value.TotalTokens != want {
			t.Fatalf("usage = %s, want total %d", got.Usage, want)
		}
	}
	submit := func(key string) store.InputReceipt {
		t.Helper()
		admission, err := s.SubmitMessage(ctx, tenant, session.ID, key, json.RawMessage(`{"text":"measure"}`))
		if err != nil {
			t.Fatal(err)
		}
		return admission
	}
	move := func(turn, from, to string) {
		t.Helper()
		if _, err := s.TransitionTurn(ctx, tenant, session.ID, turn, store.TurnTransition{ExpectedStatus: from, Status: to}); err != nil {
			t.Fatal(err)
		}
	}
	finish := func(admission store.InputReceipt, status string) {
		t.Helper()
		if _, err := s.CompleteExecution(ctx, tenant, session.ID, admission.TurnID, status, json.RawMessage(`{"done":{}}`), "", admission.Sequence); err != nil {
			t.Fatal(err)
		}
	}
	lastIdleUsage := func() json.RawMessage {
		t.Helper()
		changes, err := s.ListSessionEvents(ctx, tenant, session.ID, 0)
		if err != nil || len(changes) == 0 || changes[len(changes)-1].Event.Type != "agent.session.idle" {
			t.Fatal(changes, err)
		}
		return changes[len(changes)-1].SessionUsage
	}
	total(-1)
	measured(-1)
	first := submit("first")
	total(-1)
	move(first.TurnID, sessions.TurnQueued, sessions.TurnInProgress)
	if err = s.AppendTurnEvents(ctx, tenant, session.ID, first.TurnID, 1, usage(10)); err != nil {
		t.Fatal(err)
	}
	// An active Turn's recorded snapshot does not count yet.
	total(-1)
	measured(13)
	finish(first, sessions.TurnCompleted)
	total(13)
	if idle := lastIdleUsage(); idle == nil {
		t.Fatal("settled Session snapshot lost the known total")
	}
	// A queued, active or waiting Turn hides the known terminal totals.
	second := submit("second")
	total(-1)
	move(second.TurnID, sessions.TurnQueued, sessions.TurnInProgress)
	total(-1)
	if err = s.AppendTurnEvents(ctx, tenant, session.ID, second.TurnID, 1, usage(20)); err != nil {
		t.Fatal(err)
	}
	total(-1)
	move(second.TurnID, sessions.TurnInProgress, sessions.TurnWaiting)
	total(-1)
	measured(36)
	move(second.TurnID, sessions.TurnWaiting, sessions.TurnInProgress)
	finish(second, sessions.TurnCancelled)
	total(36)
	// A Turn that ends without usage makes the total unknown for good.
	third := submit("third")
	move(third.TurnID, sessions.TurnQueued, sessions.TurnInProgress)
	finish(third, sessions.TurnCancelled)
	total(-1)
	measured(36)
	if idle := lastIdleUsage(); idle != nil && string(idle) != "null" {
		t.Fatalf("settled Session snapshot usage: %s", idle)
	}
	fourth := submit("fourth")
	move(fourth.TurnID, sessions.TurnQueued, sessions.TurnInProgress)
	if err = s.AppendTurnEvents(ctx, tenant, session.ID, fourth.TurnID, 1, usage(30)); err != nil {
		t.Fatal(err)
	}
	finish(fourth, sessions.TurnCompleted)
	total(-1)
	measured(69)
}
