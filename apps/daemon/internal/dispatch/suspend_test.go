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
	if environmentID != "" {
		a.workspaceDirectory = "/workspace"
	}
	if r.environments != nil {
		a.environment = r.environments(ref, proto.AssignmentBindPayload{EnvironmentID: environmentID, WorkspaceDirectory: a.workspaceDirectory})
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

// suspendResult sends a quiesce or resume through Handle and returns its
// result.
func suspendResult(t *testing.T, r *Router, frames <-chan proto.Envelope, typ, id string, ref proto.AssignmentRef, request proto.EnvironmentSuspendPayload) proto.EnvironmentSuspendResultPayload {
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
			err := r.fenceEnvironment(suspendRef, proto.EnvironmentSuspendPayload{EnvironmentID: "env", SuspendID: "attempt"})
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

func TestResumeRequiresExactSuspensionAndAssignment(t *testing.T) {
	frames := make(chan proto.Envelope, 16)
	r := suspensionRouter(t, suspendSender(func(_ context.Context, env proto.Envelope) error { frames <- env; return nil }))
	request := proto.EnvironmentSuspendPayload{EnvironmentID: "env", SuspendID: "attempt"}
	foreign := suspendRef
	foreign.AssignmentID = "other"
	if got := suspendResult(t, r, frames, proto.TypeEnvironmentQuiesce, "foreign", foreign, request); got.ErrorCode != proto.AssignmentConflict {
		t.Fatalf("foreign quiesce = %+v", got)
	}
	if got := suspendResult(t, r, frames, proto.TypeEnvironmentQuiesce, "quiesce", suspendRef, request); !got.Accepted {
		t.Fatalf("quiesce = %+v", got)
	}
	// Another bound assignment did not quiesce the Environment.
	other := proto.AssignmentRef{SessionID: "other", AssignmentID: "other", Epoch: 1}
	bindAssignment(r, other, "env")
	if got := suspendResult(t, r, frames, proto.TypeEnvironmentResume, "other", other, request); got.ErrorCode != proto.AssignmentConflict {
		t.Fatalf("other assignment resume = %+v", got)
	}
	if got := suspendResult(t, r, frames, proto.TypeEnvironmentResume, "resume", suspendRef, request); !got.Accepted {
		t.Fatalf("resume = %+v", got)
	}
}

// A restarted Runtime has quiesced nothing and holds no assignment, so Core's
// resume of the Environment it quiesced before the restart succeeds.
func TestResumeOnFreshRouterSucceeds(t *testing.T) {
	frames := make(chan proto.Envelope, 1)
	r, err := New(Config{Registry: agent.NewRegistry(), Sender: suspendSender(func(_ context.Context, env proto.Envelope) error { frames <- env; return nil }), IdleTimeout: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Shutdown(context.Background()) })
	env, err := proto.NewEnvelope(proto.TypeEnvironmentResume, "resume", proto.EnvironmentSuspendPayload{EnvironmentID: "env", SuspendID: "attempt"})
	if err != nil {
		t.Fatal(err)
	}
	env.Assignment = suspendRef
	if err := r.Handle(t.Context(), env); err != nil {
		t.Fatal(err)
	}
	select {
	case frame := <-frames:
		var result proto.EnvironmentSuspendResultPayload
		if frame.Type != proto.TypeEnvironmentResumed || frame.DecodePayload(&result) != nil || !result.Accepted {
			t.Fatalf("resume = %+v %+v", frame, result)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("resume has no result")
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
		return suspendResult(t, r, frames, typ, id, ref, request)
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
	var rejected proto.ProtocolErrorPayload
	if frame := <-frames; frame.Type != proto.TypeProtocolError || frame.DecodePayload(&rejected) != nil || rejected.ErrorCode != "resource_unavailable" {
		t.Fatalf("quiesced rejection = %+v %+v", frame, rejected)
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
		"unpaused": {other, proto.EnvironmentSuspendPayload{EnvironmentID: "other", SuspendID: "attempt"}, ""},
		"obsolete": {suspendRef, proto.EnvironmentSuspendPayload{EnvironmentID: "env", SuspendID: "obsolete"}, "not_suspended"},
		"rollback": {suspendRef, proto.EnvironmentSuspendPayload{EnvironmentID: "env", SuspendID: "obsolete", Rollback: true}, "not_suspended"},
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
	frames := make(chan proto.Envelope, 16)
	r := suspensionRouter(t, suspendSender(func(_ context.Context, env proto.Envelope) error { frames <- env; return nil }))
	request := proto.EnvironmentSuspendPayload{EnvironmentID: "env", SuspendID: "attempt"}
	if got := suspendResult(t, r, frames, proto.TypeEnvironmentQuiesce, "quiesce", suspendRef, request); !got.Accepted {
		t.Fatalf("quiesce = %+v", got)
	}
	if err := r.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := suspendResult(t, r, frames, proto.TypeEnvironmentResume, "resume", suspendRef, request); got.Accepted || got.ErrorCode != "resource_busy" {
		t.Fatalf("the closed Router's resume = %+v", got)
	}
}

func TestQuiesceDrainDeadlineCannotReopenAdmission(t *testing.T) {
	r := suspensionRouter(t, suspendSender(func(context.Context, proto.Envelope) error { return nil }))
	if err := r.fenceEnvironment(suspendRef, proto.EnvironmentSuspendPayload{EnvironmentID: "env", SuspendID: "attempt"}); err != nil {
		t.Fatal(err)
	}
	work := &r.assignments[suspendRef.SessionID].work
	work.Add(1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := r.drainEnvironment(ctx, "env"); !errors.Is(err, context.Canceled) {
		t.Fatalf("drain=%v", err)
	}
	if err := r.Handle(context.Background(), proto.Envelope{Type: proto.TypeExecutionPrepare, ID: "late", Assignment: suspendRef}); !errors.Is(err, ErrRouterQuiesced) {
		t.Fatalf("deadline reopened admission: %v", err)
	}
	work.Done()
	// Starting cleanup after the drain observer timed out must not reuse a
	// sync.WaitGroup while an abandoned waiter is still returning from Wait.
	if err := r.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}
