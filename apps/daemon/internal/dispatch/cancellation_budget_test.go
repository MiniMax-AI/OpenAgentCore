package dispatch_test

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/dispatch"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto/prototest"
)

type hangingCancelExecutor struct {
	turn        *hangingCancelTurn
	closeBudget time.Duration
}

func (e *hangingCancelExecutor) StartTurn(_ context.Context, id string, _ proto.MessageInput, out chan<- proto.Envelope) (agent.Turn, error) {
	e.turn = &hangingCancelTurn{reusableTurn: &reusableTurn{id: id, out: out, settled: make(chan struct{})}}
	return e.turn, nil
}
func (e *hangingCancelExecutor) Close(ctx context.Context) error {
	deadline, _ := ctx.Deadline()
	e.closeBudget = time.Until(deadline)
	e.turn.finish()
	return nil
}

type hangingCancelTurn struct {
	*reusableTurn
	cancelBudget time.Duration
}

func (turn *hangingCancelTurn) Cancel(ctx context.Context) error {
	turn.cancels.Add(1)
	deadline, _ := ctx.Deadline()
	turn.cancelBudget = time.Until(deadline)
	<-ctx.Done()
	return ctx.Err()
}

func TestNativeCancellationTimeoutCannotBecomeAppliedAfterClose(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		owner := &hangingCancelExecutor{}
		reg := agent.NewRegistry()
		registerExecutorKind(reg, proto.SupportedAgentKind{Kind: "reusable", Available: true, Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{EnvironmentNone: proto.CapabilitySupported})}, func(context.Context, agent.PrepareRequest) (agent.Executor, error) { return owner, nil })
		sender := &recSender{}
		r, err := dispatch.New(dispatch.Config{Registry: reg, Sender: sender, IdleTimeout: time.Hour})
		if err != nil {
			t.Fatal(err)
		}
		defer r.Shutdown(t.Context())
		assign(t, r, preparationSessionID, "")
		if err := r.Handle(t.Context(), mustEnv(t, proto.TypeExecutionPrepare, "request", executorRequest())); err != nil {
			t.Fatal(err)
		}
		ready := waitPreparationStatus(t, sender, "request", "ready", "")
		if err := r.Handle(t.Context(), mustEnv(t, proto.TypeExecutionStart, "request", proto.ExecutionStartPayload{Handle: ready.Handle, ExecutorID: ready.ExecutorID, RunID: "run", Input: proto.TextInput("input")})); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if err := r.Handle(t.Context(), mustEnv(t, proto.TypePromptCancel, "run", proto.PromptCancelPayload{DeliveryID: "cancel"})); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		time.Sleep(proto.CancellationConfirmationTimeout)
		synctest.Wait()
		acks := cancellationAcks(sender)
		if len(acks) != 1 || acks[0].Applied || acks[0].Outcome != nil || (acks[0].ErrorCode != "cancel_timeout" && acks[0].ErrorCode != "cancel_failed") {
			t.Fatalf("timeout lost unknown outcome: %+v", acks)
		}
		if owner.turn.cancels.Load() != 1 || owner.turn.cancelBudget != 30*time.Second || owner.closeBudget != 10*time.Second {
			t.Fatalf("calls=%d native budget=%v close budget=%v", owner.turn.cancels.Load(), owner.turn.cancelBudget, owner.closeBudget)
		}
		if r.ActiveRuns() != 0 {
			t.Fatal("confirmed close did not release retired owner")
		}
	})
}
