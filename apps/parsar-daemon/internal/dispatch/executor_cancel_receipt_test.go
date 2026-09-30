package dispatch_test

import "github.com/MiniMax-AI-Dev/parsar/internal/harnessconfig"

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/dispatch"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto/prototest"
)

type receiptCancelSender struct {
	*recSender
	entered, release chan struct{}
	once             sync.Once
}

func (s *receiptCancelSender) Send(ctx context.Context, env proto.Envelope) error {
	if env.Type == proto.TypePromptSteerAck {
		var ack proto.PromptSteerAckPayload
		_ = env.DecodePayload(&ack)
		if ack.InputID == "pending" && !ack.Written {
			s.once.Do(func() { close(s.entered) })
			select {
			case <-s.release:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
	return s.recSender.Send(ctx, env)
}

type receiptCancelExecutor struct {
	turn                         chan *receiptCancelTurn
	current                      *receiptCancelTurn
	cancelFails, closeFailsFirst bool
	closeEntered, closeRelease   chan struct{}
	closes                       atomic.Int32
	closeOnce                    sync.Once
}

func (e *receiptCancelExecutor) StartTurn(_ context.Context, id string, _ proto.MessageInput, out chan<- proto.Envelope) (agent.Turn, error) {
	turn := &receiptCancelTurn{id: id, out: out, owner: e, written: make(chan struct{}), cancelled: make(chan struct{}), closed: make(chan struct{}), settled: make(chan struct{}), awaiting: make(chan struct{})}
	e.current = turn
	e.turn <- turn
	return turn, nil
}
func (e *receiptCancelExecutor) Close(context.Context) error {
	attempt := e.closes.Add(1)
	if e.closeFailsFirst && attempt == 1 {
		return errors.New("native close unconfirmed")
	}
	e.closeOnce.Do(func() { close(e.closeEntered) })
	<-e.closeRelease
	if e.current != nil {
		e.current.closeOnce.Do(func() { close(e.current.closed) })
		e.current.finish()
	}
	return nil
}

type receiptCancelTurn struct {
	id                                                         string
	out                                                        chan<- proto.Envelope
	owner                                                      *receiptCancelExecutor
	written, cancelled, closed, settled, awaiting              chan struct{}
	cancelOnce, closeOnce, finishOnce, terminalOnce, awaitOnce sync.Once
	cancels                                                    atomic.Int32
}

func (t *receiptCancelTurn) terminal() {
	t.terminalOnce.Do(func() { event, _ := proto.NewEnvelope(proto.TypeDone, t.id, t.CancellationOutcome()); t.out <- event })
}
func (t *receiptCancelTurn) finish() {
	t.finishOnce.Do(func() { t.terminal(); close(t.out); close(t.settled) })
}
func (t *receiptCancelTurn) Cancel(context.Context) error {
	t.cancels.Add(1)
	t.cancelOnce.Do(func() { close(t.cancelled) })
	if t.owner.cancelFails {
		return errors.New("native cancellation unconfirmed")
	}
	t.finish()
	return nil
}
func (t *receiptCancelTurn) AwaitSettlement(ctx context.Context) (agent.TurnSettlement, error) {
	t.awaitOnce.Do(func() { close(t.awaiting) })
	select {
	case <-t.settled:
		return agent.TurnSettlement{Reusable: !t.owner.cancelFails}, nil
	case <-ctx.Done():
		return agent.TurnSettlement{}, ctx.Err()
	}
}
func (t *receiptCancelTurn) CancellationOutcome() proto.DonePayload {
	return proto.DonePayload{Content: "observed", Metadata: map[string]any{proto.DoneMetaAgentSessionID: "native-session"}}
}
func (t *receiptCancelTurn) Steer(ctx context.Context, input proto.PromptSteerPayload) error {
	return t.SteerWithReceipt(ctx, input, nil)
}
func (t *receiptCancelTurn) SteerWithReceipt(ctx context.Context, _ proto.PromptSteerPayload, written func()) error {
	written()
	close(t.written)
	native := t.cancelled
	if t.owner.cancelFails {
		native = t.closed
	}
	select {
	case <-native:
	case <-ctx.Done():
		return ctx.Err()
	}
	return errors.New("native input consumption is unknown")
}

func TestExecutorCancellationReachesNativeBeforeDurableReceiptJoin(t *testing.T) {
	for _, mode := range []string{"cancel", "done_then_cancel", "cancel_failure", "close_failure_retry"} {
		t.Run(mode, func(t *testing.T) {
			sender := &receiptCancelSender{recSender: &recSender{}, entered: make(chan struct{}), release: make(chan struct{})}
			owner := &receiptCancelExecutor{turn: make(chan *receiptCancelTurn, 2), cancelFails: mode == "cancel_failure" || mode == "close_failure_retry", closeFailsFirst: mode == "close_failure_retry", closeEntered: make(chan struct{}), closeRelease: make(chan struct{})}
			reg := agent.NewRegistry()
			reg.RegisterKind(proto.SupportedAgentKind{Kind: "reusable", Available: true, Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{EnvironmentNone: proto.CapabilitySupported, DurableInputReceipts: proto.CapabilitySupported})}, harnessconfig.Configuration{}, func(context.Context, proto.PromptRequestPayload, chan<- proto.Envelope) (agent.Session, error) {
				return nil, errors.New("legacy factory forbidden")
			})
			reg.RegisterExecutor("reusable", func(context.Context, proto.PromptRequestPayload) (agent.Executor, error) { return owner, nil })
			r, err := dispatch.New(dispatch.Config{Registry: reg, Sender: sender, IdleTimeout: time.Hour})
			if err != nil {
				t.Fatal(err)
			}
			var receiptOnce, closeOnce sync.Once
			releaseReceipt := func() { receiptOnce.Do(func() { close(sender.release) }) }
			releaseClose := func() { closeOnce.Do(func() { close(owner.closeRelease) }) }
			t.Cleanup(func() {
				releaseReceipt()
				releaseClose()
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				if err := r.Shutdown(ctx); err != nil {
					t.Error(err)
				}
			})
			wait := func(ch <-chan struct{}, reason string) {
				t.Helper()
				select {
				case <-ch:
				case <-time.After(time.Second):
					t.Fatal(reason)
				}
			}
			p := executorAdmission(t, r, sender.recSender, "first", executorRequest())
			startExecutorTurn(t, r, sender.recSender, "first", "run", p)
			turn := <-owner.turn
			input := proto.PromptSteerPayload{InputID: "pending", Input: proto.TextInput("second input"), DurableReceipt: true}
			if err := r.Handle(t.Context(), mustEnv(t, proto.TypePromptSteer, "run", input)); err != nil {
				t.Fatal(err)
			}
			wait(turn.written, "input did not reach native write")
			if mode == "done_then_cancel" {
				turn.terminal()
				wait(turn.awaiting, "natural Done did not begin settlement")
			}
			cancel := func(id string) {
				t.Helper()
				if err := r.Handle(t.Context(), mustEnv(t, proto.TypePromptCancel, "run", proto.PromptCancelPayload{DeliveryID: id})); err != nil {
					t.Fatal(err)
				}
			}
			cancel("cancel")
			wait(turn.cancelled, "receipt barrier prevented native cancellation")
			acknowledgements := 0
			if mode == "close_failure_retry" {
				waitFor(t, func() bool { return len(cancellationAcks(sender.recSender)) == 1 }, "failed Close acknowledgement")
				ack := cancellationAcks(sender.recSender)[0]
				if ack.Applied || ack.Outcome != nil || r.ActiveRuns() != 1 {
					t.Fatal("failed Close released ownership or fabricated cancellation")
				}
				acknowledgements = 1
				cancel("retry")
			}
			if owner.cancelFails {
				wait(owner.closeEntered, "failed cancellation did not reach resource cleanup")
				if r.ActiveRuns() != 1 || len(cancellationAcks(sender.recSender)) != acknowledgements {
					t.Fatal("cleanup was acknowledged before resource close")
				}
				releaseClose()
			}
			wait(sender.entered, "native cancellation did not unblock the written input")
			if r.ActiveRuns() != 1 || len(cancellationAcks(sender.recSender)) != acknowledgements {
				t.Fatal("cancellation passed an uncommitted input receipt")
			}
			for _, env := range sender.snapshot() {
				if env.Type == proto.TypeDone {
					t.Fatal("Done preceded the admitted input receipt")
				}
			}
			releaseReceipt()
			waitFor(t, func() bool {
				return len(cancellationAcks(sender.recSender)) == acknowledgements+1 && r.ActiveRuns() == 0 && hasFrame(sender.recSender, proto.TypeDone, "run")
			}, "joined cancellation")
			frames := sender.snapshot()
			inputAt, doneAt, cancelAt := -1, -1, -1
			for i, env := range frames {
				switch env.Type {
				case proto.TypePromptSteerAck:
					var ack proto.PromptSteerAckPayload
					_ = env.DecodePayload(&ack)
					if !ack.Written {
						if ack.Accepted || ack.ErrorCode != "outcome_unknown" {
							t.Fatalf("unknown input fabricated receipt: %+v", ack)
						}
						inputAt = i
					}
				case proto.TypeDone:
					doneAt = i
				case proto.TypeInteractionDecisionAck:
					cancelAt = i
				}
			}
			if inputAt < 0 || doneAt < inputAt || cancelAt < inputAt || !owner.cancelFails && cancelAt < doneAt {
				t.Fatalf("receipt/Done/cancel order=%d/%d/%d", inputAt, doneAt, cancelAt)
			}
			ack := cancellationAcks(sender.recSender)[acknowledgements]
			if ack.Applied == owner.cancelFails || (ack.Outcome != nil) == owner.cancelFails {
				t.Fatalf("cancellation evidence changed during cleanup: %+v", ack)
			}
			if !owner.cancelFails {
				req := executorRequest()
				req.Configuration.AgentSessionID = "native-session"
				req.Configuration.RequireExistingNativeSession = true
				next := executorAdmission(t, r, sender.recSender, "next", req)
				if next.ExecutorID != p.ExecutorID || !next.Reused {
					t.Fatal("healthy cancellation replaced owner")
				}
				startExecutorTurn(t, r, sender.recSender, "next", "next-run", next)
				successor := <-owner.turn
				cancel("late-old-run")
				if successor.cancels.Load() != 0 || owner.closes.Load() != 0 {
					t.Fatal("old cancellation reached successor")
				}
				successor.finish()
			}
		})
	}
}
