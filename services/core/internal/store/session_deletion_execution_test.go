package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

func TestDeletedSessionWaitingTurnSettlesWithoutStoppingWorker(t *testing.T) {
	h := newFunctionHarness(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	worker := startOwnedWorker(t, ctx, h.db, h.d, h.owner())
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("worker did not stop")
		}
	}()
	input := h.message("start", "Run")
	h.read(testExecutionRequest)
	h.write(input.TurnID, proto.TypeFunctionCall, proto.FunctionCallPayload{CallID: "pending", Name: "lookup_ticket", Arguments: json.RawMessage(`{}`)})
	state := functionState(t, h, 1)
	if err := h.s.DeleteSession(ctx, h.tenant, h.session.ID); !errors.Is(err, sessions.ErrNotIdle) {
		t.Fatal("waiting Session deleted", err)
	}
	// A marker committed by an earlier release still cancels and settles work.
	if err := h.s.CommitLegacyDeletion(ctx, h.tenant, h.session.ID); err != nil {
		t.Fatal(err)
	}
	var request proto.PromptCancelPayload
	if err := h.read(proto.TypePromptCancel).DecodePayload(&request); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(sessions.FunctionResultInput{TurnID: input.TurnID, CallID: state.RequiredActions[0].CallID, Result: json.RawMessage(`{"success":true,"output":"late"}`)})
	if _, err := worker.SubmitInputs(ctx, h.tenant, h.session.ID, "late", []sessions.Input{{Kind: "tool_result", Payload: raw}}); !errors.Is(err, sessions.ErrNotFound) {
		t.Fatal(err)
	}
	h.write(input.TurnID, proto.TypeInteractionDecisionAck, proto.InteractionDecisionAckPayload{DeliveryID: request.DeliveryID, Applied: true, Outcome: &proto.DonePayload{Metadata: map[string]any{proto.DoneMetaAgentSessionID: "deleted-native"}}})
	waitTurn(t, h, input.TurnID, sessions.TurnCancelled)
	bound, err := h.s.GetSessionExecutionBinding(ctx, h.tenant, h.session.ID)
	if err != nil || bound.NativeSessionID != "deleted-native" {
		t.Fatal(bound, err)
	}
	if _, err := h.s.GetSession(ctx, h.tenant, h.session.ID); !errors.Is(err, sessions.ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := h.bound().Run(ctx, h.tenant, h.session.ID, input.TurnID); !errors.Is(err, sessions.ErrNotFound) {
		t.Fatal("deleted Session must be unavailable to new dispatch", err)
	}
	h.session = publicSession(t, h, "unrelated")
	next := h.message("next", "Unrelated work")
	h.read(testExecutionRequest)
	h.write(next.TurnID, proto.TypeDone, proto.DonePayload{Content: "unaffected"})
	waitTurn(t, h, next.TurnID, sessions.TurnCompleted)
}

func TestDeletedSessionRestartStillReconcilesHiddenClaim(t *testing.T) {
	h := newDispatchHarness(t)
	input := h.message("interrupted", "Run")
	ctx := t.Context()
	if _, err := h.s.TransitionTurn(ctx, h.tenant, h.session.ID, input.TurnID, sessions.TurnTransition{ExpectedStatus: sessions.TurnQueued, Status: sessions.TurnInProgress}); err != nil {
		t.Fatal(err)
	}
	if err := h.s.DeleteSession(ctx, h.tenant, h.session.ID); !errors.Is(err, sessions.ErrNotIdle) {
		t.Fatal("running Session deleted", err)
	}
	if err := h.s.CommitLegacyDeletion(ctx, h.tenant, h.session.ID); err != nil {
		t.Fatal(err)
	}
	worker := startWorker(t, ctx, h.db, h.d)
	stopped, cancel := context.WithCancel(ctx)
	cancel()
	if err := worker.Run(stopped); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	turn, err := h.s.GetTurn(ctx, h.tenant, h.session.ID, input.TurnID)
	if err != nil || turn.Status != sessions.TurnFailed {
		t.Fatal(turn, err)
	}
	if _, err := h.s.GetSession(ctx, h.tenant, h.session.ID); !errors.Is(err, sessions.ErrNotFound) {
		t.Fatal(err)
	}
}

// A caller deletes running work by cancelling first, waiting for the Session to
// settle and then deleting. The rejected deletion leaves the waiting Turn intact.
func TestWaitingSessionCancelsThenDeletesThroughWorker(t *testing.T) {
	h := newFunctionHarness(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	worker := startWorker(t, ctx, h.db, h.d)
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("worker did not stop")
		}
	}()
	input := h.message("start", "Run")
	h.read(testExecutionRequest)
	h.write(input.TurnID, proto.TypeFunctionCall, proto.FunctionCallPayload{CallID: "pending", Name: "lookup_ticket", Arguments: json.RawMessage(`{}`)})
	state := functionState(t, h, 1)
	if err := h.s.DeleteSession(ctx, h.tenant, h.session.ID); !errors.Is(err, sessions.ErrNotIdle) {
		t.Fatal("waiting Session deleted", err)
	}
	if again := functionState(t, h, 1); again.LastTurn == nil || again.LastTurn.Status != sessions.TurnWaiting || !again.LastTurn.CancelRequestedAt.IsZero() {
		t.Fatal("rejected deletion changed required actions", again)
	}
	if _, err := h.s.RequestCancel(ctx, h.tenant, h.session.ID, "cancel-before-delete"); err != nil {
		t.Fatal(err)
	}
	// The explicit cancellation, not the rejected deletion, reaches the daemon.
	var request proto.PromptCancelPayload
	if err := h.read(proto.TypePromptCancel).DecodePayload(&request); err != nil {
		t.Fatal(err)
	}
	if err := h.s.DeleteSession(ctx, h.tenant, h.session.ID); !errors.Is(err, sessions.ErrNotIdle) {
		t.Fatal("Session deleted before cancellation settled", err)
	}
	h.write(input.TurnID, proto.TypeInteractionDecisionAck, proto.InteractionDecisionAckPayload{DeliveryID: request.DeliveryID, Applied: true, Outcome: &proto.DonePayload{Metadata: map[string]any{proto.DoneMetaAgentSessionID: "cancelled-native"}}})
	waitTurn(t, h, input.TurnID, sessions.TurnCancelled)
	deadline := time.Now().Add(10 * time.Second)
	for {
		err := h.s.DeleteSession(ctx, h.tenant, h.session.ID)
		if err == nil {
			break
		}
		if !errors.Is(err, sessions.ErrNotIdle) || time.Now().After(deadline) {
			t.Fatal("settled Session not deleted", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := h.s.DeleteSession(ctx, h.tenant, h.session.ID); err != nil {
		t.Fatal("repeated deletion", err)
	}
	raw, _ := json.Marshal(sessions.FunctionResultInput{TurnID: input.TurnID, CallID: state.RequiredActions[0].CallID, Result: json.RawMessage(`{"success":true,"output":"late"}`)})
	if _, err := worker.SubmitInputs(ctx, h.tenant, h.session.ID, "late", []sessions.Input{{Kind: "tool_result", Payload: raw}}); !errors.Is(err, sessions.ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := h.s.GetSession(ctx, h.tenant, h.session.ID); !errors.Is(err, sessions.ErrNotFound) {
		t.Fatal(err)
	}
	h.session = publicSession(t, h, "unrelated")
	next := h.message("next", "Unrelated work")
	h.read(testExecutionRequest)
	h.write(next.TurnID, proto.TypeDone, proto.DonePayload{Content: "unaffected"})
	waitTurn(t, h, next.TurnID, sessions.TurnCompleted)
}
