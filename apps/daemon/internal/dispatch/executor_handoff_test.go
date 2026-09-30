package dispatch

import "github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig"

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto/prototest"
)

type terminalHandoffSender struct {
	frames  chan proto.Envelope
	visible chan struct{}
	release chan struct{}
	failure error
}

func (s *terminalHandoffSender) Send(ctx context.Context, env proto.Envelope) error {
	select {
	case s.frames <- env:
	case <-ctx.Done():
		return ctx.Err()
	}
	if env.Type == proto.TypeDone && env.ID == "first" {
		close(s.visible)
		select {
		case <-s.release:
			return s.failure
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

type terminalHandoffExecutor struct {
	turns  chan *terminalHandoffTurn
	closes atomic.Int32
}

func (e *terminalHandoffExecutor) Close(context.Context) error { e.closes.Add(1); return nil }
func (e *terminalHandoffExecutor) StartTurn(_ context.Context, id string, _ proto.MessageInput, out chan<- proto.Envelope) (agent.Turn, error) {
	turn := &terminalHandoffTurn{id: id, out: out, settled: make(chan struct{})}
	e.turns <- turn
	return turn, nil
}

type terminalHandoffTurn struct {
	id      string
	out     chan<- proto.Envelope
	settled chan struct{}
	once    sync.Once
}

func (t *terminalHandoffTurn) finish() {
	t.once.Do(func() {
		env, _ := proto.NewEnvelope(proto.TypeDone, t.id, t.CancellationOutcome())
		t.out <- env
		close(t.out)
		close(t.settled)
	})
}
func (t *terminalHandoffTurn) Cancel(context.Context) error { t.finish(); return nil }
func (t *terminalHandoffTurn) CancellationOutcome() proto.DonePayload {
	return proto.DonePayload{Content: t.id, Metadata: map[string]any{proto.DoneMetaAgentSessionID: "native"}}
}
func (t *terminalHandoffTurn) AwaitSettlement(ctx context.Context) (agent.TurnSettlement, error) {
	select {
	case <-t.settled:
		return agent.TurnSettlement{Reusable: true}, nil
	case <-ctx.Done():
		return agent.TurnSettlement{}, ctx.Err()
	}
}

func TestPreparedDonePublishesAfterExecutorHandoff(t *testing.T) {
	for _, fail := range []bool{false, true} {
		name := "delivered"
		if fail {
			name = "late_delivery_error"
		}
		t.Run(name, func(t *testing.T) {
			sender := &terminalHandoffSender{frames: make(chan proto.Envelope, 32), visible: make(chan struct{}), release: make(chan struct{})}
			if fail {
				sender.failure = errors.New("old terminal delivery failed")
			}
			owner := &terminalHandoffExecutor{turns: make(chan *terminalHandoffTurn, 3)}
			registry := agent.NewRegistry()
			registry.RegisterKind(proto.SupportedAgentKind{Kind: "handoff", Available: true, Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{EnvironmentNone: proto.CapabilitySupported})},
				harnessconfig.Configuration{}, func(context.Context, proto.PromptRequestPayload, chan<- proto.Envelope) (agent.Session, error) {
					return nil, errors.New("legacy path forbidden")
				})
			var creates atomic.Int32
			registry.RegisterExecutor("handoff", func(context.Context, proto.PromptRequestPayload) (agent.Executor, error) {
				creates.Add(1)
				return owner, nil
			})
			r, err := New(Config{Registry: registry, Sender: sender, IdleTimeout: time.Hour})
			if err != nil {
				t.Fatal(err)
			}
			var releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(sender.release) }) }
			t.Cleanup(func() {
				unblock()
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				if err := r.Shutdown(ctx); err != nil {
					t.Error(err)
				}
			})
			handle := func(kind, id string, payload any) {
				t.Helper()
				env, err := proto.NewEnvelope(kind, id, payload)
				if err != nil {
					t.Fatal(err)
				}
				if err = r.Handle(t.Context(), env); err != nil {
					t.Fatal(err)
				}
			}
			status := func(id, state string) proto.PreparationStatusPayload {
				t.Helper()
				deadline := time.After(time.Second)
				for {
					select {
					case env := <-sender.frames:
						if env.Type != proto.TypePreparationStatus || env.ID != id {
							continue
						}
						var value proto.PreparationStatusPayload
						if err := env.DecodePayload(&value); err != nil {
							t.Fatal(err)
						}
						if value.State == state {
							return value
						}
						if value.State == "rejected" || value.State == "failed" {
							t.Fatalf("next admission failed: %+v", value)
						}
					case <-deadline:
						t.Fatal("preparation status missing")
					}
				}
			}
			request := proto.ExecutionPreparePayload{SessionID: "session", Configuration: proto.PromptRequestPayload{AgentKind: "handoff", AgentStateKey: "agents-api-session", StrictResume: true, DisableExecutionEnvironment: true}}
			admit := func(id string) proto.PreparationStatusPayload {
				t.Helper()
				handle(proto.TypeExecutionPrepare, id, request)
				return status(id, "ready")
			}
			start := func(id string, p proto.PreparationStatusPayload) *terminalHandoffTurn {
				t.Helper()
				handle(proto.TypeExecutionStart, id, proto.ExecutionStartPayload{Handle: p.Handle, ExecutorID: p.ExecutorID, RunID: id, Input: proto.TextInput(id)})
				status(id, "started")
				select {
				case turn := <-owner.turns:
					return turn
				case <-time.After(time.Second):
					t.Fatal("native Turn missing")
					return nil
				}
			}
			first := admit("first")
			old := start("first", first)
			r.mu.Lock()
			handoff := r.sessions["first"].preparedHandoff
			r.mu.Unlock()
			old.finish()
			select {
			case <-sender.visible:
			case <-time.After(time.Second):
				t.Fatal("Done not visible")
			}
			// Simulate Core admitting its successor after seeing Done, while the old
			// Send is still blocked. Exact native continuity must already be committed.
			request.Configuration.AgentSessionID = "native"
			request.Configuration.RequireExistingNativeSession = true
			second := admit("second")
			if second.ExecutorID != first.ExecutorID || !second.Reused || creates.Load() != 1 {
				t.Fatal("settled owner was replaced")
			}
			next := start("second", second)
			unblock()
			r.mu.Lock()
			settled := handoff.release.settled
			r.mu.Unlock()
			select {
			case <-settled:
			case <-time.After(time.Second):
				t.Fatal("old publication did not finish")
			}
			r.mu.Lock()
			current := r.executors["session"]
			valid := current != nil && !current.invalid && current.run == r.sessions["second"] && current.admission == r.preparations[second.Handle]
			deliveryErr := handoff.outputErr
			r.mu.Unlock()
			if !valid || r.ActiveRuns() != 1 || owner.closes.Load() != 0 {
				t.Fatal("old terminal cleanup damaged its successor")
			}
			if (deliveryErr != nil) != fail {
				t.Fatalf("delivery error=%v", deliveryErr)
			}
			next.finish()
			// The second terminal does not block: observing it must likewise permit an
			// immediate third admission without losing or recreating the retained owner.
			for {
				select {
				case env := <-sender.frames:
					if env.Type == proto.TypeDone && env.ID == "second" {
						goto completed
					}
				case <-time.After(time.Second):
					t.Fatal("second Done missing")
				}
			}
		completed:
			third := admit("third")
			if third.ExecutorID != first.ExecutorID || !third.Reused || creates.Load() != 1 {
				t.Fatal("successor failed to retain owner")
			}
		})
	}
}

// These fixtures exercise settlement only; active input is deliberately rejected.
func (*terminalHandoffTurn) SteerWithReceipt(context.Context, proto.PromptSteerPayload, func()) error {
	return agent.ErrSteeringRejected
}
