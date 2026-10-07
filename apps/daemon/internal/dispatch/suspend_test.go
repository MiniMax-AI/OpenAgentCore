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

var suspendRef = proto.AssignmentRef{SessionID: "session", AssignmentID: "assignment", Epoch: 1}

// bindAssignment records ref as bound in environmentID, as assignment_bind does.
func bindAssignment(r *Router, ref proto.AssignmentRef, environmentID string) {
	r.mu.Lock()
	a := &assignmentState{ref: ref, environmentID: environmentID}
	if r.environments != nil {
		a.environment = r.environments(ref, proto.AssignmentBindPayload{EnvironmentID: environmentID})
	}
	r.assignments[ref.SessionID] = a
	r.mu.Unlock()
}

type suspendedExecutor struct{ closed atomic.Int32 }

func (e *suspendedExecutor) StartTurn(context.Context, string, proto.MessageInput, chan<- proto.Envelope) (agent.Turn, error) {
	return nil, errors.New("suspended executor must not start a Turn")
}

func (e *suspendedExecutor) Close(context.Context) error { e.closed.Add(1); return nil }

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
	session := suspendRef.SessionID
	cases := map[string]func(*Router){
		"active":              func(r *Router) { r.sessions["run"] = &sessionState{environmentID: "env"} },
		"preparing":           func(r *Router) { r.preparations["p"] = &preparationState{owns: true, environmentID: "env"} },
		"receipt":             func(r *Router) { r.preparations["p"] = &preparationState{busy: true, environmentID: "env"} },
		"read":                func(r *Router) { r.workspaceReads = map[string]string{"read": session} },
		"write":               func(r *Router) { r.workspaceWrites[session] = &workspaceUpload{} },
		"export":              func(r *Router) { r.workspaceExports[session] = &workspaceExport{} },
		"runtime preparation": func(r *Router) { r.runtimePreparations[session] = &runtimePreparationTransfer{} },
		"executor":            func(r *Router) { r.executors[session] = &executorState{environmentID: "env", preparing: true} },
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
			clear(r.workspaceWrites)
			clear(r.workspaceExports)
			clear(r.runtimePreparations)
			clear(r.executors)
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
		parked := r.suspensions["env"] != nil
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

func TestQuiescingOneEnvironmentLeavesAnotherRunning(t *testing.T) {
	frames := make(chan proto.Envelope, 16)
	r := suspensionRouter(t, suspendSender(func(_ context.Context, env proto.Envelope) error { frames <- env; return nil }))
	other := proto.AssignmentRef{SessionID: "other", AssignmentID: "other", Epoch: 1}
	bindAssignment(r, other, "other")
	quiesced, running := &suspendedExecutor{}, &suspendedExecutor{}
	r.mu.Lock()
	r.executors[suspendRef.SessionID] = &executorState{id: "quiesced", sessionID: suspendRef.SessionID, environmentID: "env", native: quiesced, cancel: func() {}}
	r.executors[other.SessionID] = &executorState{id: "running", sessionID: other.SessionID, environmentID: "other", native: running, cancel: func() {}}
	// The other Environment's Session has a Run in progress.
	r.sessions["run"] = &sessionState{assignment: other, environmentID: "other"}
	r.mu.Unlock()
	suspend := func(typ, id string, ref proto.AssignmentRef, request proto.EnvironmentSuspendPayload) proto.EnvironmentSuspendResultPayload {
		t.Helper()
		env, err := proto.NewEnvelope(typ, id, request)
		if err != nil {
			t.Fatal(err)
		}
		env.Assignment = ref
		if err := r.Handle(t.Context(), env); err != nil {
			t.Fatal(err)
		}
		for {
			select {
			case frame := <-frames:
				var result proto.EnvironmentSuspendResultPayload
				if frame.ID == id && frame.DecodePayload(&result) == nil {
					return result
				}
			case <-time.After(3 * time.Second):
				t.Fatalf("%s %s has no result", typ, id)
			}
		}
	}
	prepare := func(ref proto.AssignmentRef) error {
		return r.Handle(t.Context(), proto.Envelope{Type: proto.TypeExecutionPrepare, ID: "prepare", Assignment: ref})
	}
	request := proto.EnvironmentSuspendPayload{EnvironmentID: "env", SuspendID: "attempt"}
	if got := suspend(proto.TypeEnvironmentQuiesce, "quiesce", suspendRef, request); !got.Accepted {
		t.Fatalf("quiesce = %+v", got)
	}
	if quiesced.closed.Load() != 1 || running.closed.Load() != 0 {
		t.Fatalf("closed Executors: quiesced %d, running %d", quiesced.closed.Load(), running.closed.Load())
	}
	if err := prepare(suspendRef); !errors.Is(err, ErrRouterQuiesced) {
		t.Fatalf("the quiesced Environment admitted %v", err)
	}
	if err := prepare(other); errors.Is(err, ErrRouterQuiesced) {
		t.Fatal("the other Environment stopped admitting work")
	}
	for id, test := range map[string]struct {
		ref     proto.AssignmentRef
		request proto.EnvironmentSuspendPayload
		code    string
	}{
		"foreign":  {other, request, proto.AssignmentConflict},
		"unpaused": {other, proto.EnvironmentSuspendPayload{EnvironmentID: "other", SuspendID: "attempt"}, "not_suspended"},
		"rollback": {other, proto.EnvironmentSuspendPayload{EnvironmentID: "other", SuspendID: "attempt", Rollback: true}, ""},
		"obsolete": {suspendRef, proto.EnvironmentSuspendPayload{EnvironmentID: "env", SuspendID: "obsolete"}, "not_suspended"},
	} {
		if got := suspend(proto.TypeEnvironmentResume, id, test.ref, test.request); got.ErrorCode != test.code || got.Accepted != (test.code == "") {
			t.Fatalf("%s resume = %+v", id, got)
		}
	}
	if got := suspend(proto.TypeEnvironmentResume, "resume", suspendRef, request); !got.Accepted {
		t.Fatalf("resume = %+v", got)
	}
	if err := prepare(suspendRef); errors.Is(err, ErrRouterQuiesced) {
		t.Fatal("resume did not reopen the Environment")
	}
	r.mu.Lock()
	_, run := r.sessions["run"]
	// The synthetic Run owns no goroutine; remove it before cleanup.
	clear(r.sessions)
	r.mu.Unlock()
	if !run || running.closed.Load() != 0 {
		t.Fatal("quiescing one Environment ended another's work")
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
