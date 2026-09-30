package dispatch

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

type suspendSender func(context.Context, proto.Envelope) error

func (s suspendSender) Send(ctx context.Context, env proto.Envelope) error { return s(ctx, env) }

type suspendedSession struct{ cancelled atomic.Int32 }

func (s *suspendedSession) Cancel(context.Context) error { s.cancelled.Add(1); return nil }

func suspensionRouter(t *testing.T, sender Sender) *Router {
	t.Helper()
	r, err := New(Config{Registry: agent.NewRegistry(), Sender: sender, IdleTimeout: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := r.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return r
}

func TestQuiesceRejectsEveryUnsettledResource(t *testing.T) {
	cases := map[string]func(*Router){
		"active":     func(r *Router) { r.sessions["run"] = &sessionState{ctxCancel: func() {}} },
		"preparing":  func(r *Router) { r.preparations["p"] = &preparationState{owns: true} },
		"receipt":    func(r *Router) { r.preparations["p"] = &preparationState{busy: true} },
		"read":       func(r *Router) { r.workspaceReads = map[string]struct{}{"read": {}} },
		"write":      func(r *Router) { r.workspaceWrite = &workspaceUpload{} },
		"export":     func(r *Router) { r.workspaceExport = &workspaceExport{} },
		"permission": func(r *Router) { r.permIndex["permission"] = "run" },
		"choice":     func(r *Router) { r.askIndex["choice"] = "run" },
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			r := suspensionRouter(t, suspendSender(func(context.Context, proto.Envelope) error { return nil }))
			setup(r)
			err := r.Quiesce(context.Background(), proto.EnvironmentSuspendPayload{EnvironmentID: "env", SuspendID: "attempt"})
			if !errors.Is(err, ErrRouterBusy) {
				t.Fatalf("quiesce=%v", err)
			}
			// These synthetic resources own no goroutine; remove them before cleanup.
			r.sessions = map[string]*sessionState{}
			r.preparations = map[string]*preparationState{}
			r.workspaceReads = nil
			r.workspaceWrite = nil
			r.workspaceExport = nil
		})
	}
}

func TestQuiesceDrainsPendingReceiptAndFencesConcurrentAdmission(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	r := suspensionRouter(t, suspendSender(func(context.Context, proto.Envelope) error { close(entered); <-release; return nil }))
	// Rejection receipts run independently of the preparation resource map.
	_ = r.Handle(context.Background(), proto.Envelope{Type: proto.TypeExecutionPrepare, ID: "invalid"})
	<-entered
	request := proto.EnvironmentSuspendPayload{EnvironmentID: "env", SuspendID: "attempt"}
	quiet := make(chan error, 1)
	go func() { quiet <- r.Quiesce(context.Background(), request) }()
	deadline := time.After(time.Second)
	for {
		r.mu.Lock()
		parked := r.suspension != nil
		r.mu.Unlock()
		if parked {
			break
		}
		select {
		case <-deadline:
			t.Fatal("quiesce did not fence admission")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	admitted := make(chan error, 1)
	go func() {
		admitted <- r.Handle(context.Background(), proto.Envelope{Type: proto.TypePromptRequest, ID: "late"})
	}()
	select {
	case err := <-quiet:
		t.Fatalf("acknowledged before receipt settled: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	if err := <-quiet; err != nil {
		t.Fatal(err)
	}
	if err := <-admitted; !errors.Is(err, ErrRouterQuiesced) {
		t.Fatalf("new admission = %v", err)
	}
}

func TestQuiescePreservesIdleOwnerAgainstExpiredTimerAndRequiresExactResume(t *testing.T) {
	sender := suspendSender(func(context.Context, proto.Envelope) error { return nil })
	r := suspensionRouter(t, sender)
	session := &suspendedSession{}
	state := &sessionState{runID: "run", environmentID: "env", stateKey: "state", session: session, ctxCancel: func() {}, retain: true}
	r.mu.Lock()
	r.idle["state"] = map[*sessionState]struct{}{state: {}}
	r.scheduleIdleLocked(state)
	oldLease := state.idleLease
	r.mu.Unlock()
	request := proto.EnvironmentSuspendPayload{EnvironmentID: "env", SuspendID: "attempt"}
	if err := r.Quiesce(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	r.expireIdle(state, oldLease)
	if session.cancelled.Load() != 0 {
		t.Fatal("pre-snapshot timer killed retained owner")
	}
	wrong := request
	wrong.SuspendID = "obsolete"
	if err := r.Resume(wrong, sender); err == nil {
		t.Fatal("stale operation reopened admission")
	}
	if err := r.Resume(request, sender); err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	newLease := state.idleLease
	r.mu.Unlock()
	r.expireIdle(state, newLease)
	if session.cancelled.Load() != 1 {
		t.Fatal("normal idle expiration was not restored")
	}
}

func TestShutdownDestroysQuiescedOwnerAndCannotResume(t *testing.T) {
	sender := suspendSender(func(context.Context, proto.Envelope) error { return nil })
	r := suspensionRouter(t, sender)
	request := proto.EnvironmentSuspendPayload{EnvironmentID: "env", SuspendID: "attempt"}
	if err := r.Quiesce(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if err := r.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(r.Resume(request, sender), ErrRouterClosed) {
		t.Fatal("closed Router resurrected")
	}
}

func TestQuiesceDrainDeadlineCannotReopenAdmission(t *testing.T) {
	r := suspensionRouter(t, suspendSender(func(context.Context, proto.Envelope) error { return nil }))
	r.shutdownWG.Add(1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := r.Quiesce(ctx, proto.EnvironmentSuspendPayload{EnvironmentID: "env", SuspendID: "attempt"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("quiesce=%v", err)
	}
	if err := r.Handle(context.Background(), proto.Envelope{Type: proto.TypePromptRequest, ID: "late"}); !errors.Is(err, ErrRouterQuiesced) {
		t.Fatalf("deadline reopened admission: %v", err)
	}
	r.shutdownWG.Done()
	// Starting cleanup after the drain observer timed out must not reuse a
	// sync.WaitGroup while an abandoned waiter is still returning from Wait.
	if err := r.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}
