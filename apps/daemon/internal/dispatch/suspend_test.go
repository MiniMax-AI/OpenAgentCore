package dispatch

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

type suspendSender func(context.Context, proto.Envelope) error

func (s suspendSender) Send(ctx context.Context, env proto.Envelope) error { return s(ctx, env) }

var suspendRef = proto.AssignmentRef{SessionID: "session", AssignmentID: "assignment", Epoch: 1}

// bindAssignment records ref as bound in environmentID, as assignment_bind does.
func bindAssignment(r *Router, ref proto.AssignmentRef, environmentID string) {
	r.mu.Lock()
	r.assignments[ref.SessionID] = &assignmentState{ref: ref, environmentID: environmentID}
	r.mu.Unlock()
}

func suspensionRouter(t *testing.T, sender Sender) *Router {
	t.Helper()
	r, err := New(Config{Registry: agent.NewRegistry(), Sender: sender, IdleTimeout: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	bindAssignment(r, suspendRef, "env")
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
			err := r.Quiesce(context.Background(), suspendRef, proto.EnvironmentSuspendPayload{EnvironmentID: "env", SuspendID: "attempt"})
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
	go func() { quiet <- r.Quiesce(context.Background(), suspendRef, request) }()
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
		admitted <- r.Handle(context.Background(), proto.Envelope{Type: proto.TypeExecutionPrepare, ID: "late", Assignment: suspendRef})
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

func TestResumeRequiresExactSuspensionAndAssignment(t *testing.T) {
	sender := suspendSender(func(context.Context, proto.Envelope) error { return nil })
	r := suspensionRouter(t, sender)
	request := proto.EnvironmentSuspendPayload{EnvironmentID: "env", SuspendID: "attempt"}
	foreign := suspendRef
	foreign.AssignmentID = "other"
	if err := r.Quiesce(context.Background(), foreign, request); !errors.Is(err, AssignmentError(proto.AssignmentConflict)) {
		t.Fatalf("foreign quiesce = %v", err)
	}
	if err := r.Quiesce(context.Background(), suspendRef, request); err != nil {
		t.Fatal(err)
	}
	wrong := request
	wrong.SuspendID = "obsolete"
	if err := r.Resume(suspendRef, wrong, sender); err == nil {
		t.Fatal("stale operation reopened admission")
	}
	if err := r.Resume(foreign, request, sender); err == nil {
		t.Fatal("foreign assignment reopened admission")
	}
	// Another bound assignment did not quiesce the Runtime.
	other := proto.AssignmentRef{SessionID: "other", AssignmentID: "other", Epoch: 1}
	bindAssignment(r, other, "env")
	if err := r.Resume(other, request, sender); !errors.Is(err, AssignmentError(proto.AssignmentConflict)) {
		t.Fatalf("other assignment resume = %v", err)
	}
	if err := r.Resume(suspendRef, request, sender); err != nil {
		t.Fatal(err)
	}
}

func TestShutdownDestroysQuiescedOwnerAndCannotResume(t *testing.T) {
	sender := suspendSender(func(context.Context, proto.Envelope) error { return nil })
	r := suspensionRouter(t, sender)
	request := proto.EnvironmentSuspendPayload{EnvironmentID: "env", SuspendID: "attempt"}
	if err := r.Quiesce(context.Background(), suspendRef, request); err != nil {
		t.Fatal(err)
	}
	if err := r.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(r.Resume(suspendRef, request, sender), ErrRouterClosed) {
		t.Fatal("closed Router resurrected")
	}
}

func TestQuiesceDrainDeadlineCannotReopenAdmission(t *testing.T) {
	r := suspensionRouter(t, suspendSender(func(context.Context, proto.Envelope) error { return nil }))
	r.shutdownWG.Add(1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := r.Quiesce(ctx, suspendRef, proto.EnvironmentSuspendPayload{EnvironmentID: "env", SuspendID: "attempt"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("quiesce=%v", err)
	}
	if err := r.Handle(context.Background(), proto.Envelope{Type: proto.TypeExecutionPrepare, ID: "late", Assignment: suspendRef}); !errors.Is(err, ErrRouterQuiesced) {
		t.Fatalf("deadline reopened admission: %v", err)
	}
	r.shutdownWG.Done()
	// Starting cleanup after the drain observer timed out must not reuse a
	// sync.WaitGroup while an abandoned waiter is still returning from Wait.
	if err := r.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}
