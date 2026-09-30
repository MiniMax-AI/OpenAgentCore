// Package contracttest provides shared assertions for Harness adapter tests.
// Fixtures may use controlled native transports or real providers; the caller
// must state which. A controlled transport is not live native qualification.
package contracttest

import (
	"context"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

// TextFixture supplies a prepared Executor and deterministic native inputs.
// CompleteInput finishes normally; ActiveInput emits a Ready observation and
// stays active for steering and cancellation. NativeOwner observes the adapter's
// retained native owner: a native Session, reusable SDK Query or owned process,
// as appropriate for that adapter. It must not report just the Go wrapper. This
// does not require every Harness to retain one process or one transport; native
// per-Turn transports may change while their Session owner remains the same.
// Neither callback may start, finish or replace a Turn on behalf of the adapter.
type TextFixture struct {
	Executor                                  agent.Executor
	CompleteInput, ActiveInput, SteeringInput proto.MessageInput
	Ready                                     func(proto.Envelope) bool
	NativeOwner                               func() string
}

// TextLifecycle verifies ordinary reuse, exact Turn ownership, durable active
// input, confirmed cancellation and continuation of a healthy Executor. It asks
// for no optional tools, images, interactions, workspace or detailed Usage.
// Native fault injection and history recovery have separate adapter tests.
func TextLifecycle(t *testing.T, fixture TextFixture) {
	t.Helper()
	if fixture.Executor == nil || fixture.Ready == nil || fixture.NativeOwner == nil {
		t.Fatal("text contract fixture requires executor, readiness and native ownership")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := fixture.Executor.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	first, output := start(t, ctx, fixture, "contract-first", fixture.CompleteInput)
	native := settled(t, ctx, first, output, true)
	owner := fixture.NativeOwner()
	if native == "" || owner == "" {
		t.Fatal("missing observed native history or resource identity")
	}
	second, output := start(t, ctx, fixture, "contract-active", fixture.ActiveInput)
	select {
	case <-output.ready:
	case <-ctx.Done():
		t.Fatal("active Turn never became ready")
	}
	if sameTurn(first, second) {
		t.Fatal("StartTurn reused the previous Turn object")
	}
	if err := first.Cancel(ctx); err != nil {
		t.Fatal(err)
	}
	steerer, ok := second.(agent.DurableSteerer)
	if !ok {
		t.Fatal("public text Turn must implement DurableSteerer")
	}
	var written atomic.Int32
	if err := steerer.SteerWithReceipt(ctx, proto.PromptSteerPayload{InputID: "contract-input", Input: fixture.SteeringInput, DurableReceipt: true}, func() { written.Add(1) }); err != nil {
		t.Fatalf("active input after stale cancellation: %v", err)
	}
	if written.Load() != 1 {
		t.Fatalf("expected one complete-write callback, got %d", written.Load())
	}
	if err := second.Cancel(ctx); err != nil {
		t.Fatal(err)
	}
	if got := settled(t, ctx, second, output, false); got != native {
		t.Fatalf("cancellation changed history: %q", got)
	}
	third, output := start(t, ctx, fixture, "contract-after-cancel", fixture.CompleteInput)
	if sameTurn(third, first) || sameTurn(third, second) {
		t.Fatal("StartTurn reused a settled Turn object")
	}
	if got := settled(t, ctx, third, output, true); got != native {
		t.Fatalf("continuation changed history: %q", got)
	}
	if fixture.NativeOwner() != owner {
		t.Fatal("healthy Turns replaced the native owner")
	}
}

type captured struct {
	id            string
	ready, closed chan struct{}
	events        []proto.Envelope // Read only after closed, which synchronizes the drain.
}

func start(t *testing.T, ctx context.Context, f TextFixture, id string, input proto.MessageInput) (agent.Turn, *captured) {
	t.Helper()
	out := make(chan proto.Envelope, 64)
	capture := &captured{id: id, ready: make(chan struct{}), closed: make(chan struct{})}
	go func() {
		defer close(capture.closed)
		var ready sync.Once
		for event := range out {
			capture.events = append(capture.events, event)
			if f.Ready(event) {
				ready.Do(func() { close(capture.ready) })
			}
		}
	}()
	turn, err := f.Executor.StartTurn(ctx, id, input, out)
	if turn == nil {
		close(out)
	}
	if err != nil || turn == nil {
		t.Fatalf("StartTurn(%s): %v", id, err)
	}
	return turn, capture
}

func settled(t *testing.T, ctx context.Context, turn agent.Turn, output *captured, normal bool) string {
	t.Helper()
	state, err := turn.AwaitSettlement(ctx)
	if err != nil || !state.Reusable {
		t.Fatalf("healthy Turn did not settle for reuse: %+v, %v", state, err)
	}
	select {
	case <-output.closed:
	case <-ctx.Done():
		t.Fatal("settled Turn retained its output stream")
	}
	terminals := 0
	var done proto.DonePayload
	for _, event := range output.events {
		if event.ID != output.id {
			t.Fatalf("event belongs to %q, expected %q", event.ID, output.id)
		}
		if terminals != 0 {
			t.Fatal("event followed terminal outcome")
		}
		if event.Type == proto.TypeError && normal {
			t.Fatal("normal Turn emitted an error")
		}
		if event.Type == proto.TypeDone {
			terminals++
			if err := event.DecodePayload(&done); err != nil {
				t.Fatal(err)
			}
		}
	}
	if normal && terminals != 1 {
		t.Fatalf("normal Turn emitted %d terminal outcomes", terminals)
	}
	if !normal {
		done = turn.CancellationOutcome()
	}
	id, _ := done.Metadata[proto.DoneMetaAgentSessionID].(string)
	return id
}

func sameTurn(a, b agent.Turn) bool {
	return reflect.ValueOf(a).Comparable() && a == b
}
