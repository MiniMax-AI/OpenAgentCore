package dispatch_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/dispatch"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

// frameFor returns the last frame of kind correlated with id.
func frameFor(sender *recSender, kind, id string) (proto.Envelope, bool) {
	frames := sender.snapshot()
	for i := len(frames) - 1; i >= 0; i-- {
		if frames[i].Type == kind && frames[i].ID == id {
			return frames[i], true
		}
	}
	return proto.Envelope{}, false
}

func waitAssignmentStatus(t *testing.T, sender *recSender, id string) proto.AssignmentStatusPayload {
	t.Helper()
	waitFor(t, func() bool { return hasFrame(sender, proto.TypeAssignmentStatus, id) }, "assignment_status "+id)
	frame, _ := frameFor(sender, proto.TypeAssignmentStatus, id)
	var status proto.AssignmentStatusPayload
	if err := frame.DecodePayload(&status); err != nil {
		t.Fatal(err)
	}
	return status
}

// release sends assignment_release under the session's assignment at epoch.
func release(t *testing.T, r *dispatch.Router, session, id string, epoch uint64, removeHome bool) {
	t.Helper()
	env := scoped(t, session, proto.TypeAssignmentRelease, id, proto.AssignmentReleasePayload{RemoveHome: removeHome})
	env.Assignment.Epoch = epoch
	if err := r.Handle(t.Context(), env); err != nil {
		t.Fatal(err)
	}
}

// observedExecutor runs closed before it closes.
type observedExecutor struct {
	*reusableExecutor
	closed func()
}

func (e *observedExecutor) Close(ctx context.Context) error {
	e.closed()
	return e.reusableExecutor.Close(ctx)
}

func TestAssignmentRejectsStaleAndForeignFrames(t *testing.T) {
	h := newHarness(t)
	defer h.router.Shutdown(context.Background())
	assign(t, h.router, "s", "")
	release(t, h.router, "s", "release", 2, false)
	if got := waitAssignmentStatus(t, h.sender, "release"); got.State != proto.AssignmentReleased || got.ErrorCode != "" {
		t.Fatalf("release = %+v", got)
	}
	foreign := ref("s")
	foreign.AssignmentID = "foreign"
	prepare := proto.ExecutionPreparePayload{SessionID: "s", Configuration: proto.PromptRequestPayload{AgentKind: "fake_alpha", AgentStateKey: stateKey("s"), StrictResume: true, DisableExecutionEnvironment: true}}
	for id, test := range map[string]struct {
		ref  proto.AssignmentRef
		code string
	}{
		"stale":   {ref("s"), proto.AssignmentStale},
		"foreign": {foreign, proto.AssignmentConflict},
	} {
		env := scoped(t, "s", proto.TypeExecutionPrepare, id, prepare)
		env.Assignment = test.ref
		if err := h.router.Handle(t.Context(), env); err == nil {
			t.Fatalf("%s preparation admitted", id)
		}
		if got := waitPreparationStatus(t, h.sender, id, "rejected", ""); got.ErrorCode != test.code {
			t.Fatalf("%s preparation = %+v", id, got)
		}
		bind := scoped(t, "s", proto.TypeAssignmentBind, id+"-bind", proto.AssignmentBindPayload{})
		bind.Assignment = test.ref
		if err := h.router.Handle(t.Context(), bind); err != nil {
			t.Fatal(err)
		}
		if got := waitAssignmentStatus(t, h.sender, id+"-bind"); got.State != proto.AssignmentFailed || got.ErrorCode != test.code {
			t.Fatalf("%s bind = %+v", id, got)
		}
	}
}

func TestAssignmentReleaseWaitsForRacingPreparation(t *testing.T) {
	var calls atomic.Int32
	var sender *recSender
	var replyBeforeClose atomic.Bool
	owner := &observedExecutor{reusableExecutor: &reusableExecutor{}, closed: func() {
		replyBeforeClose.Store(hasFrame(sender, proto.TypeAssignmentStatus, "release"))
	}}
	entered, cancelled, unblock := make(chan struct{}), make(chan struct{}), make(chan struct{})
	r, sender := poolRouter(t, func(ctx context.Context, _ proto.PromptRequestPayload) (agent.Executor, error) {
		calls.Add(1)
		close(entered)
		<-ctx.Done()
		close(cancelled)
		<-unblock
		return owner, nil
	})
	defer close(unblock)
	assign(t, r, preparationSessionID, "")
	if err := r.Handle(t.Context(), scoped(t, preparationSessionID, proto.TypeExecutionPrepare, "prepare", executorRequest())); err != nil {
		t.Fatal(err)
	}
	<-entered
	release(t, r, preparationSessionID, "release", 2, false)
	select {
	case <-cancelled:
	case <-time.After(3 * time.Second):
		t.Fatal("release did not cancel the preparation")
	}
	if hasFrame(sender, proto.TypeAssignmentStatus, "release") {
		t.Fatal("release replied before the Executor closed")
	}
	unblock <- struct{}{}
	if got := waitAssignmentStatus(t, sender, "release"); got.State != proto.AssignmentReleased || got.ErrorCode != "" {
		t.Fatalf("release = %+v", got)
	}
	if owner.closes.Load() != 1 || replyBeforeClose.Load() {
		t.Fatalf("Executor closes = %d, replied before close = %t", owner.closes.Load(), replyBeforeClose.Load())
	}
	for _, frame := range sender.snapshot() {
		var status proto.PreparationStatusPayload
		if frame.Type == proto.TypePreparationStatus && frame.ID == "prepare" && frame.DecodePayload(&status) == nil && status.State == "ready" {
			t.Fatal("released preparation became ready")
		}
	}
	late := scoped(t, preparationSessionID, proto.TypeExecutionPrepare, "late", executorRequest())
	late.Assignment.Epoch = 2
	if err := r.Handle(t.Context(), late); err == nil {
		t.Fatal("released assignment admitted a preparation")
	}
	if got := waitPreparationStatus(t, sender, "late", "rejected", ""); got.ErrorCode != proto.AssignmentStale || calls.Load() != 1 {
		t.Fatalf("late preparation = %+v, factory calls = %d", got, calls.Load())
	}
}

func TestReleaseWithoutHomeRemovalKeepsAssignment(t *testing.T) {
	h := newHarness(t)
	defer h.router.Shutdown(context.Background())
	assign(t, h.router, "s", "")
	release(t, h.router, "s", "remove", 2, true)
	if got := waitAssignmentStatus(t, h.sender, "remove"); got.State != proto.AssignmentFailed || got.ErrorCode != proto.UnsupportedOperation {
		t.Fatalf("release = %+v", got)
	}
	startRun(t, h.router, h.sender, "fake_alpha", "s")
}

func TestUnknownEnvelopeGetsCorrelatedProtocolError(t *testing.T) {
	h := newHarness(t)
	defer h.router.Shutdown(context.Background())
	if err := h.router.Handle(t.Context(), scoped(t, "s", "future_operation", "request", nil)); err != nil {
		t.Fatal(err)
	}
	frame, ok := frameFor(h.sender, proto.TypeProtocolError, "request")
	var got proto.ProtocolErrorPayload
	if !ok || frame.DecodePayload(&got) != nil || got != (proto.ProtocolErrorPayload{Type: "future_operation", ErrorCode: proto.UnsupportedOperation}) || frame.Assignment != ref("s") {
		t.Fatalf("protocol_error = %+v %+v", frame, got)
	}
}
