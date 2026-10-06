package store

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

func TestCancellationStaysBoundToItsOriginalTurn(t *testing.T) {
	s, _ := testStore(t)
	tenant, session := newTurnSession(t, s)
	ctx := context.Background()
	idle, err := s.RequestCancel(ctx, tenant, session.ID, "idle-cancel")
	if err != nil || idle.TurnID != "" || idle.Sequence == 0 {
		t.Fatalf("idle cancellation: %+v, %v", idle, err)
	}
	first := submitMessage(t, s, tenant, session.ID, "first")
	started := transition(t, s, tenant, session.ID, first.TurnID, sessions.TurnQueued, sessions.TurnInProgress)
	if started.StartedAt.IsZero() || !started.CompletedAt.IsZero() {
		t.Fatalf("started timestamps: %+v", started)
	}
	cancel, err := s.RequestCancel(ctx, tenant, session.ID, "cancel")
	if err != nil || cancel.TurnID != first.TurnID {
		t.Fatalf("cancellation target: %+v, %v", cancel, err)
	}
	pending, err := sessionAdapter(s).GetTurn(ctx, tenant, session.ID, first.TurnID)
	if err != nil || pending.Status != sessions.TurnInProgress || pending.CancelRequestedAt.IsZero() || !pending.CompletedAt.IsZero() {
		t.Fatalf("running cancellation falsely completed: %+v, %v", pending, err)
	}
	transition(t, s, tenant, session.ID, first.TurnID, sessions.TurnInProgress, sessions.TurnCancelled)
	next := submitMessage(t, s, tenant, session.ID, "next")
	for key, original := range map[string]sessions.InputReceipt{"cancel": cancel, "idle-cancel": idle} {
		retry, err := s.RequestCancel(ctx, tenant, session.ID, key)
		if err != nil || !retry.Replayed || retry.TurnID != original.TurnID || retry.Sequence != original.Sequence {
			t.Fatalf("cancellation retargeted: %+v, %v", retry, err)
		}
	}
	queued, err := sessionAdapter(s).GetTurn(ctx, tenant, session.ID, next.TurnID)
	if err != nil || queued.Status != sessions.TurnQueued || !queued.CancelRequestedAt.IsZero() {
		t.Fatalf("old cancellation affected later Turn: %+v, %v", queued, err)
	}
	if _, err := s.RequestCancel(ctx, tenant, session.ID, "cancel-queued"); err != nil {
		t.Fatal(err)
	}
	stopped, err := sessionAdapter(s).GetTurn(ctx, tenant, session.ID, next.TurnID)
	if err != nil || stopped.Status != sessions.TurnCancelled || stopped.CompletedAt.IsZero() || !stopped.StartedAt.IsZero() {
		t.Fatalf("queued work did not stop: %+v, %v", stopped, err)
	}
	if _, err := transitionTurn(ctx, s, tenant, session.ID, next.TurnID, sessions.TurnTransition{ExpectedStatus: sessions.TurnQueued, Status: sessions.TurnInProgress}); !errors.Is(err, sessions.ErrTurnConflict) {
		t.Fatalf("cancelled queued work was started: %v", err)
	}
}

func TestWaitingTurnRetainsInputsAndStartTime(t *testing.T) {
	s, _ := testStore(t)
	tenant, session := newTurnSession(t, s)
	ctx := context.Background()
	first := submitMessage(t, s, tenant, session.ID, "first")
	started := transition(t, s, tenant, session.ID, first.TurnID, sessions.TurnQueued, sessions.TurnInProgress)
	transition(t, s, tenant, session.ID, first.TurnID, sessions.TurnInProgress, sessions.TurnWaiting)
	steer := submitMessage(t, s, tenant, session.ID, "steer")
	if steer.TurnID != first.TurnID {
		t.Fatal("waiting input started another Turn")
	}
	resumed := transition(t, s, tenant, session.ID, first.TurnID, sessions.TurnWaiting, sessions.TurnInProgress)
	if !started.StartedAt.Equal(resumed.StartedAt) {
		t.Fatal("resume reset the start time")
	}
	transition(t, s, tenant, session.ID, first.TurnID, sessions.TurnInProgress, sessions.TurnWaiting)
	if _, err := s.RequestCancel(ctx, tenant, session.ID, "cancel"); err != nil {
		t.Fatal(err)
	}
	if _, err := transitionTurn(ctx, s, tenant, session.ID, first.TurnID, sessions.TurnTransition{ExpectedStatus: sessions.TurnWaiting, Status: sessions.TurnInProgress}); !errors.Is(err, sessions.ErrTurnConflict) {
		t.Fatalf("cancelling Turn resumed: %v", err)
	}
	transition(t, s, tenant, session.ID, first.TurnID, sessions.TurnWaiting, sessions.TurnCancelled)
}

func TestTurnInputValidationHasNoSideEffects(t *testing.T) {
	s, _ := testStore(t)
	tenant, session := newTurnSession(t, s)
	ctx := context.Background()
	for _, raw := range []json.RawMessage{nil, json.RawMessage(`[]`), json.RawMessage(`null`), json.RawMessage(`{} {}`), json.RawMessage(`{"text":"` + string(make([]byte, 512*1024)) + `"}`)} {
		if _, err := s.SubmitMessage(ctx, tenant, session.ID, "first", raw); !errors.Is(err, sessions.ErrInvalidInput) {
			t.Fatalf("invalid input accepted: %v", err)
		}
	}
	cancelled, stop := context.WithCancel(ctx)
	stop()
	if _, err := s.SubmitMessage(cancelled, tenant, session.ID, "first", messagePayload); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled context: %v", err)
	}
	first := submitMessage(t, s, tenant, session.ID, "first")
	if first.Replayed {
		t.Fatal("failed submission persisted a receipt")
	}
	for _, input := range []sessions.TurnTransition{
		{ExpectedStatus: sessions.TurnQueued, Status: sessions.TurnCompleted},
		{ExpectedStatus: sessions.TurnInProgress, Status: sessions.TurnQueued},
		{ExpectedStatus: sessions.TurnCompleted, Status: sessions.TurnInProgress},
		{ExpectedStatus: sessions.TurnQueued, Status: sessions.TurnInProgress, Outcome: json.RawMessage(`{"premature":true}`)},
	} {
		if _, err := transitionTurn(ctx, s, tenant, session.ID, first.TurnID, input); !errors.Is(err, sessions.ErrInvalidInput) {
			t.Fatalf("invalid transition accepted: %v", err)
		}
	}
	got, err := sessionAdapter(s).GetTurn(ctx, tenant, session.ID, first.TurnID)
	if err != nil || got.Status != sessions.TurnQueued || !got.StartedAt.IsZero() {
		t.Fatalf("invalid transition changed Turn: %+v, %v", got, err)
	}
}
