package dispatch_test

import "github.com/MiniMax-AI-Dev/parsar/internal/harnessconfig"

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/dispatch"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto/prototest"
)

type reusableExecutor struct {
	starts   chan *reusableTurn
	closes   atomic.Int32
	mu       sync.Mutex
	closeErr error
	startErr error
}

func (e *reusableExecutor) Close(context.Context) error {
	e.closes.Add(1)
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.closeErr
}
func (e *reusableExecutor) StartTurn(_ context.Context, id string, _ proto.MessageInput, out chan<- proto.Envelope) (agent.Turn, error) {
	if e.startErr != nil {
		return nil, e.startErr
	}
	t := &reusableTurn{id: id, out: out, settled: make(chan struct{})}
	e.starts <- t
	return t, nil
}

type reusableTurn struct {
	id      string
	out     chan<- proto.Envelope
	settled chan struct{}
	once    sync.Once
	cancels atomic.Int32
}

func (t *reusableTurn) finish() {
	t.once.Do(func() {
		frame, _ := proto.NewEnvelope(proto.TypeDone, t.id, t.CancellationOutcome())
		t.out <- frame
		close(t.out)
		close(t.settled)
	})
}
func (t *reusableTurn) Cancel(context.Context) error { t.cancels.Add(1); t.finish(); return nil }
func (t *reusableTurn) CancellationOutcome() proto.DonePayload {
	return proto.DonePayload{Content: t.id, Metadata: map[string]any{proto.DoneMetaAgentSessionID: "native-session"}}
}
func (t *reusableTurn) AwaitSettlement(ctx context.Context) (agent.TurnSettlement, error) {
	select {
	case <-t.settled:
		return agent.TurnSettlement{Reusable: true}, nil
	case <-ctx.Done():
		return agent.TurnSettlement{}, ctx.Err()
	}
}

func executorRequest() proto.ExecutionPreparePayload {
	return proto.ExecutionPreparePayload{SessionID: "session", Configuration: proto.PromptRequestPayload{AgentKind: "reusable", AgentStateKey: "agents-api-session", StrictResume: true, DisableExecutionEnvironment: true}}
}
func executorRouter(t *testing.T, owner *reusableExecutor, idle time.Duration) (*dispatch.Router, *recSender, *atomic.Int32) {
	t.Helper()
	calls := &atomic.Int32{}
	reg := agent.NewRegistry()
	reg.RegisterKind(proto.SupportedAgentKind{Kind: "reusable", Available: true, Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{EnvironmentNone: proto.CapabilitySupported, DurableInputReceipts: proto.CapabilitySupported})}, harnessconfig.Configuration{}, func(context.Context, proto.PromptRequestPayload, chan<- proto.Envelope) (agent.Session, error) {
		return nil, errors.New("ordinary factory is forbidden")
	})
	reg.RegisterExecutor("reusable", func(context.Context, proto.PromptRequestPayload) (agent.Executor, error) {
		calls.Add(1)
		return owner, nil
	})
	sender := &recSender{}
	router, err := dispatch.New(dispatch.Config{Registry: reg, Sender: sender, IdleTimeout: idle})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		owner.mu.Lock()
		owner.closeErr = nil
		owner.mu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := router.Shutdown(ctx); err != nil {
			t.Error(err)
		}
	})
	return router, sender, calls
}
func executorAdmission(t *testing.T, r *dispatch.Router, s *recSender, key string, req proto.ExecutionPreparePayload) proto.PreparationStatusPayload {
	t.Helper()
	if err := r.Handle(t.Context(), mustEnv(t, proto.TypeExecutionPrepare, key, req)); err != nil {
		t.Fatal(err)
	}
	return waitPreparationStatus(t, s, key, "ready", "")
}
func startExecutorTurn(t *testing.T, r *dispatch.Router, s *recSender, key, id string, p proto.PreparationStatusPayload) {
	t.Helper()
	if err := r.Handle(t.Context(), mustEnv(t, proto.TypeExecutionStart, key, proto.ExecutionStartPayload{Handle: p.Handle, ExecutorID: p.ExecutorID, RunID: id, Input: proto.TextInput("input")})); err != nil {
		t.Fatal(err)
	}
	waitPreparationStatus(t, s, key, "started", "")
}
func TestExecutorRetainsOwnerAcrossIndependentTurns(t *testing.T) {
	owner := &reusableExecutor{starts: make(chan *reusableTurn, 2)}
	r, s, calls := executorRouter(t, owner, time.Minute)
	first := executorAdmission(t, r, s, "first", executorRequest())
	startExecutorTurn(t, r, s, "first", "one", first)
	turn := <-owner.starts
	turn.finish()
	waitFor(t, func() bool { return r.ActiveRuns() == 0 }, "first Turn settled")
	if owner.closes.Load() != 0 || turn.cancels.Load() != 0 {
		t.Fatal("normal completion tore down or cancelled the executor")
	}
	_ = r.Handle(t.Context(), mustEnv(t, proto.TypeExecutionRelease, "first", proto.ExecutionReleasePayload{Handle: first.Handle}))
	req := executorRequest()
	req.Configuration.AgentSessionID = "native-session"
	req.Configuration.RequireExistingNativeSession = true
	second := executorAdmission(t, r, s, "second", req)
	if second.Handle == first.Handle || second.ExecutorID != first.ExecutorID || !second.Reused || calls.Load() != 1 {
		t.Fatalf("reuse identities: first=%+v second=%+v calls=%d", first, second, calls.Load())
	}
	startExecutorTurn(t, r, s, "second", "two", second)
	next := <-owner.starts
	_ = turn.Cancel(t.Context())
	if next.cancels.Load() != 0 {
		t.Fatal("old Turn cancellation reached its successor")
	}
	next.finish()
	waitFor(t, func() bool { return r.ActiveRuns() == 0 }, "second Turn settled")
	if owner.closes.Load() != 0 {
		t.Fatal("executor closed between Turns")
	}
}
func TestExecutorCancelSettlesTurnWithoutClosingOwner(t *testing.T) {
	owner := &reusableExecutor{starts: make(chan *reusableTurn, 1)}
	r, s, _ := executorRouter(t, owner, time.Minute)
	p := executorAdmission(t, r, s, "first", executorRequest())
	startExecutorTurn(t, r, s, "first", "run", p)
	turn := <-owner.starts
	if err := r.Handle(t.Context(), mustEnv(t, proto.TypePromptCancel, "run", proto.PromptCancelPayload{DeliveryID: "cancel"})); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return len(cancellationAcks(s)) == 1 }, "cancellation receipt")
	ack := cancellationAcks(s)[0]
	if !ack.Applied || ack.Outcome == nil || turn.cancels.Load() != 1 || owner.closes.Load() != 0 {
		t.Fatalf("cancel=%+v closes=%d", ack, owner.closes.Load())
	}
}
func TestExecutorRejectsConfigurationAndNativeIdentityChanges(t *testing.T) {
	owner := &reusableExecutor{starts: make(chan *reusableTurn, 1)}
	r, s, calls := executorRouter(t, owner, time.Minute)
	p := executorAdmission(t, r, s, "first", executorRequest())
	startExecutorTurn(t, r, s, "first", "one", p)
	(<-owner.starts).finish()
	waitFor(t, func() bool { return r.ActiveRuns() == 0 }, "Turn settlement")
	for _, kind := range []string{"configuration", "native"} {
		req := executorRequest()
		if kind == "configuration" {
			req.Configuration.AgentOptions = map[string]any{"model": "changed"}
		} else {
			req.Configuration.AgentSessionID = "other-native"
		}
		if err := r.Handle(t.Context(), mustEnv(t, proto.TypeExecutionPrepare, kind, req)); err == nil {
			t.Fatalf("accepted changed %s", kind)
		}
	}
	if calls.Load() != 1 || owner.closes.Load() != 0 {
		t.Fatal("conflict replaced executor")
	}
}
func TestExecutorIdleExpiryClosesRetainedOwner(t *testing.T) {
	owner := &reusableExecutor{starts: make(chan *reusableTurn, 1)}
	r, s, _ := executorRouter(t, owner, 30*time.Millisecond)
	p := executorAdmission(t, r, s, "first", executorRequest())
	startExecutorTurn(t, r, s, "first", "one", p)
	(<-owner.starts).finish()
	waitFor(t, func() bool { return owner.closes.Load() == 1 }, "idle executor close")
}
func TestExecutorPreInputFailureConfirmsCloseBeforeRetrySignal(t *testing.T) {
	for _, unconfirmed := range []bool{false, true} {
		t.Run(map[bool]string{false: "closed", true: "unconfirmed"}[unconfirmed], func(t *testing.T) {
			owner := &reusableExecutor{starts: make(chan *reusableTurn, 1), startErr: errors.New("native exited")}
			if unconfirmed {
				owner.closeErr = errors.New("cleanup unknown")
			}
			r, s, _ := executorRouter(t, owner, time.Minute)
			p := executorAdmission(t, r, s, "first", executorRequest())
			_ = r.Handle(t.Context(), mustEnv(t, proto.TypeExecutionStart, "first", proto.ExecutionStartPayload{Handle: p.Handle, ExecutorID: p.ExecutorID, RunID: "run", Input: proto.TextInput("input")}))
			status := waitPreparationStatus(t, s, "first", "rejected", "")
			want := "executor_unavailable"
			if unconfirmed {
				want = "executor_cleanup_unconfirmed"
			}
			if status.ErrorCode != want || owner.closes.Load() == 0 {
				t.Fatalf("status=%+v closes=%d", status, owner.closes.Load())
			}
			if unconfirmed {
				if err := r.Handle(t.Context(), mustEnv(t, proto.TypeExecutionPrepare, "retry", executorRequest())); err == nil {
					t.Fatal("replaced unconfirmed owner")
				}
			}
		})
	}
}

func poolRouter(t *testing.T, factory agent.ExecutorFactory) (*dispatch.Router, *recSender) {
	t.Helper()
	registry := agent.NewRegistry()
	registry.RegisterKind(proto.SupportedAgentKind{Kind: "reusable", Available: true, Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{EnvironmentNone: proto.CapabilitySupported, DurableInputReceipts: proto.CapabilitySupported})}, harnessconfig.Configuration{}, func(context.Context, proto.PromptRequestPayload, chan<- proto.Envelope) (agent.Session, error) {
		return nil, errors.New("unexpected legacy factory")
	})
	registry.RegisterExecutor("reusable", factory)
	sender := &recSender{}
	router, err := dispatch.New(dispatch.Config{Registry: registry, Sender: sender, IdleTimeout: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := router.Shutdown(ctx); err != nil {
			t.Error(err)
		}
	})
	return router, sender
}
func poolRequest(id string) proto.ExecutionPreparePayload {
	req := executorRequest()
	req.SessionID = id
	req.Configuration.AgentStateKey = "agents-api-" + id
	return req
}

func TestExecutorIdleAndActiveCapacitiesAreIndependent(t *testing.T) {
	var mu sync.Mutex
	var owners []*reusableExecutor
	r, s := poolRouter(t, func(context.Context, proto.PromptRequestPayload) (agent.Executor, error) {
		e := &reusableExecutor{}
		mu.Lock()
		owners = append(owners, e)
		mu.Unlock()
		return e, nil
	})
	for i := 0; i < 17; i++ {
		id := fmt.Sprint("idle-", i)
		ready := executorAdmission(t, r, s, id, poolRequest(id))
		if err := r.Handle(t.Context(), mustEnv(t, proto.TypeExecutionRelease, id, proto.ExecutionReleasePayload{Handle: ready.Handle})); err != nil {
			t.Fatal(err)
		}
	}
	waitFor(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		var closed int32
		for _, e := range owners {
			closed += e.closes.Load()
		}
		return closed == 1
	}, "idle capacity eviction")
	for i := 0; i < 4; i++ {
		id := fmt.Sprint("active-", i)
		executorAdmission(t, r, s, id, poolRequest(id))
	}
	if err := r.Handle(t.Context(), mustEnv(t, proto.TypeExecutionPrepare, "overflow", poolRequest("overflow"))); err == nil {
		t.Fatal("active capacity exceeded")
	}
}

func TestExecutorRejectsSessionStateScopeMismatch(t *testing.T) {
	e := &reusableExecutor{}
	r, s, calls := executorRouter(t, e, time.Minute)
	executorAdmission(t, r, s, "one", executorRequest())
	req := executorRequest()
	req.SessionID = "another"
	if err := r.Handle(t.Context(), mustEnv(t, proto.TypeExecutionPrepare, "scope", req)); err == nil {
		t.Fatal("two sessions shared native state")
	}
	if calls.Load() != 1 {
		t.Fatal("mismatched session invoked native preparation")
	}
}

func TestExecutorRejectsOutputFromAnotherTurn(t *testing.T) {
	e := &reusableExecutor{starts: make(chan *reusableTurn, 1)}
	r, s, _ := executorRouter(t, e, time.Minute)
	p := executorAdmission(t, r, s, "one", executorRequest())
	startExecutorTurn(t, r, s, "one", "current", p)
	turn := <-e.starts
	wrong, _ := proto.NewEnvelope(proto.TypeDelta, "old-turn", proto.DeltaPayload{Delta: "late"})
	turn.out <- wrong
	turn.finish()
	waitFor(t, func() bool { return r.ActiveRuns() == 0 }, "invalid native event cleanup")
	if e.closes.Load() != 1 {
		t.Fatal("invalid event left executor reusable")
	}
	for _, frame := range s.snapshot() {
		if frame.ID == "old-turn" {
			t.Fatal("old Turn output escaped")
		}
	}
}

func (*reusableTurn) SteerWithReceipt(context.Context, proto.PromptSteerPayload, func()) error {
	return agent.ErrSteeringRejected
}
