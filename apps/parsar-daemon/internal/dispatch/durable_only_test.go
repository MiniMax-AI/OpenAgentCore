package dispatch_test

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

type durableOnlyExecutor struct {
	*reusableExecutor
	applied atomic.Int32
}

func (e *durableOnlyExecutor) StartTurn(ctx context.Context, id string, input proto.MessageInput, out chan<- proto.Envelope) (agent.Turn, error) {
	turn, err := e.reusableExecutor.StartTurn(ctx, id, input, out)
	return &durableOnlyTurn{Turn: turn, applied: &e.applied}, err
}

type durableOnlyTurn struct {
	agent.Turn
	applied *atomic.Int32
}

func (t *durableOnlyTurn) SteerWithReceipt(_ context.Context, _ proto.PromptSteerPayload, written func()) error {
	written()
	t.applied.Add(1)
	return nil
}

func TestPublicTextSteeringDoesNotRequireOptionalSteerer(t *testing.T) {
	owner := &durableOnlyExecutor{reusableExecutor: &reusableExecutor{starts: make(chan *reusableTurn, 1)}}
	router, sender := poolRouter(t, func(context.Context, proto.PromptRequestPayload) (agent.Executor, error) { return owner, nil })
	admission := executorAdmission(t, router, sender, "prepare", executorRequest())
	startExecutorTurn(t, router, sender, "prepare", "run", admission)
	turn := <-owner.starts
	defer turn.finish()
	input := proto.PromptSteerPayload{InputID: "extra", Input: proto.TextInput("follow-up"), DurableReceipt: true}
	if err := router.Handle(t.Context(), mustEnv(t, proto.TypePromptSteer, "run", input)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		for _, event := range sender.snapshot() {
			var ack proto.PromptSteerAckPayload
			if event.Type == proto.TypePromptSteerAck && event.DecodePayload(&ack) == nil && ack.InputID == input.InputID && ack.Accepted {
				return true
			}
		}
		return false
	}, "durable input applied without optional Steerer")
	if owner.applied.Load() != 1 {
		t.Fatal("input was not applied exactly once")
	}
	input.DurableReceipt = false
	input.InputID = "ordinary"
	if err := router.Handle(t.Context(), mustEnv(t, proto.TypePromptSteer, "run", input)); err != nil {
		t.Fatal(err)
	}
	for _, event := range sender.snapshot() {
		var ack proto.PromptSteerAckPayload
		if event.Type == proto.TypePromptSteerAck && event.DecodePayload(&ack) == nil && ack.InputID == "ordinary" && ack.ErrorCode == "unsupported" {
			return
		}
	}
	t.Fatal("unimplemented optional Steerer was not rejected")
}
