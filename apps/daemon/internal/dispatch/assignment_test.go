package dispatch_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/dispatch"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxbootstrap"
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
	prepare := noEnvironmentPreparation("s", proto.PromptRequestPayload{AgentKind: "fake_alpha"})
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

func TestAssignmentBindCarriesLink(t *testing.T) {
	h := newHarness(t)
	defer h.router.Shutdown(context.Background())
	environment := uuid.NewString()
	resource := &sandboxbootstrap.Resource{TenantID: uuid.NewString(), EnvironmentID: environment, Kind: "allocation", ID: uuid.NewString(), Generation: 1}
	other := *resource
	other.EnvironmentID = uuid.NewString()
	bind := func(id string, payload proto.AssignmentBindPayload) proto.AssignmentStatusPayload {
		if err := h.router.Handle(t.Context(), scoped(t, "s", proto.TypeAssignmentBind, id, payload)); err != nil {
			t.Fatal(err)
		}
		return waitAssignmentStatus(t, h.sender, id)
	}
	for id, payload := range map[string]proto.AssignmentBindPayload{
		"no grant":          {EnvironmentID: environment, Resource: resource},
		"no resource":       {EnvironmentID: environment, AttachGrant: []byte("grant")},
		"other environment": {EnvironmentID: environment, Resource: &other, AttachGrant: []byte("grant")},
	} {
		if got := bind(id, payload); got.ErrorCode != "invalid_request" {
			t.Fatalf("%s: bind = %+v", id, got)
		}
	}
	link := proto.AssignmentBindPayload{EnvironmentID: environment, Resource: resource, AttachGrant: []byte("grant")}
	if got := bind("bind", link); got.State != proto.AssignmentBound {
		t.Fatalf("bind = %+v", got)
	}
	if got := bind("again", link); got.State != proto.AssignmentBound {
		t.Fatalf("identical bind = %+v", got)
	}
	changed := link
	changed.AttachGrant = []byte("other grant")
	if got := bind("changed", changed); got.ErrorCode != proto.AssignmentConflict {
		t.Fatalf("bind with another grant = %+v", got)
	}
}

// closingOwner counts its Closes and fails if two overlap. The first Close
// waits for release.
type closingOwner struct {
	dispatch.Environment
	closes, active atomic.Int32
	overlapped     atomic.Bool
	entered        chan struct{}
	release        chan struct{}
}

func (o *closingOwner) Close(context.Context) error {
	if o.active.Add(1) > 1 {
		o.overlapped.Store(true)
	}
	defer o.active.Add(-1)
	if o.closes.Add(1) == 1 && o.entered != nil {
		close(o.entered)
		<-o.release
	}
	return nil
}

func TestReleaseRetryAndShutdownCloseOwnersOnce(t *testing.T) {
	const other = "33333333-3333-4333-8333-333333333333"
	released := &closingOwner{entered: make(chan struct{}), release: make(chan struct{})}
	unreleased := &closingOwner{}
	sender := &recSender{}
	r, err := dispatch.New(dispatch.Config{Registry: agent.NewRegistry(), Sender: sender, Environments: func(ref proto.AssignmentRef, _ proto.AssignmentBindPayload) dispatch.Environment {
		if ref.SessionID == other {
			return unreleased
		}
		return released
	}})
	if err != nil {
		t.Fatal(err)
	}
	assign(t, r, preparationSessionID, preparationEnvironmentID)
	assign(t, r, other, preparationEnvironmentID)
	release(t, r, preparationSessionID, "release", 2, false)
	<-released.entered
	release(t, r, preparationSessionID, "retry", 2, false)
	close(released.release)
	for _, id := range []string{"release", "retry"} {
		if got := waitAssignmentStatus(t, sender, id); got.State != proto.AssignmentReleased {
			t.Fatalf("%s = %+v", id, got)
		}
	}
	if err := r.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
	if released.closes.Load() != 2 || released.overlapped.Load() || unreleased.closes.Load() != 1 {
		t.Fatalf("released owner closes = %d (overlapped %t), unreleased owner closes = %d", released.closes.Load(), released.overlapped.Load(), unreleased.closes.Load())
	}
}

func TestSupersedingBindFencesTheEarlierEpoch(t *testing.T) {
	h := newHarness(t)
	defer h.router.Shutdown(context.Background())
	startRun(t, h.router, h.sender, "fake_alpha", "s")
	sess := <-h.gotSess
	bind := func(id string, epoch uint64, payload proto.AssignmentBindPayload) proto.AssignmentStatusPayload {
		env := scoped(t, "s", proto.TypeAssignmentBind, id, payload)
		env.Assignment.Epoch = epoch
		if err := h.router.Handle(t.Context(), env); err != nil {
			t.Fatal(err)
		}
		return waitAssignmentStatus(t, h.sender, id)
	}
	if got := bind("supersede", 2, proto.AssignmentBindPayload{}); got.State != proto.AssignmentBound || got.ErrorCode != "" {
		t.Fatalf("superseding bind = %+v", got)
	}
	// The earlier epoch's Run ended before the bind replied.
	terminal, bound := -1, -1
	for i, frame := range h.sender.snapshot() {
		switch {
		case frame.ID == "s" && (frame.Type == proto.TypeDone || frame.Type == proto.TypeError):
			terminal = i
		case frame.ID == "supersede":
			bound = i
		}
	}
	if terminal < 0 || terminal > bound || sess.cancels() != 1 {
		t.Fatalf("Run terminal at %d, bind reply at %d, cancels = %d", terminal, bound, sess.cancels())
	}
	for id, test := range map[string]struct {
		epoch   uint64
		payload proto.AssignmentBindPayload
		code    string
	}{
		"lower":    {1, proto.AssignmentBindPayload{}, proto.AssignmentStale},
		"changed":  {2, proto.AssignmentBindPayload{EnvironmentID: uuid.NewString()}, proto.AssignmentConflict},
		"repeated": {2, proto.AssignmentBindPayload{}, ""},
	} {
		if got := bind(id, test.epoch, test.payload); got.ErrorCode != test.code {
			t.Fatalf("%s bind = %+v", id, got)
		}
	}
	stale := scoped(t, "s", proto.TypeExecutionPrepare, "stale", noEnvironmentPreparation("s", proto.PromptRequestPayload{AgentKind: "fake_alpha"}))
	if err := h.router.Handle(t.Context(), stale); err == nil {
		t.Fatal("the superseded epoch admitted a preparation")
	}
	if got := waitPreparationStatus(t, h.sender, "stale", "rejected", ""); got.ErrorCode != proto.AssignmentStale {
		t.Fatalf("superseded preparation = %+v", got)
	}
	current := stale
	current.ID, current.Assignment.Epoch = "current", 2
	if err := h.router.Handle(t.Context(), current); err != nil {
		t.Fatal(err)
	}
	waitPreparationStatus(t, h.sender, "current", "ready", "")
}
