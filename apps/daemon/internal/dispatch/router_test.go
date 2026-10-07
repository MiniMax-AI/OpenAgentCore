package dispatch_test

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/dispatch"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto/prototest"
)

// ---------------------------------------------------------------------
// test doubles
// ---------------------------------------------------------------------

// recSender records every Envelope. failNow makes the next Send fail.
type recSender struct {
	mu      sync.Mutex
	frames  []proto.Envelope
	failNow bool
	hold    func(proto.Envelope) // runs before a frame is sent
}

func (s *recSender) Send(_ context.Context, env proto.Envelope) error {
	if s.hold != nil {
		s.hold(env)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failNow {
		s.failNow = false
		return errors.New("sender broken")
	}
	s.frames = append(s.frames, env)
	return nil
}

func (s *recSender) snapshot() []proto.Envelope {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]proto.Envelope, len(s.frames))
	copy(out, s.frames)
	return out
}

func (s *recSender) typesFor(runID string) []string {
	out := []string{}
	for _, f := range s.snapshot() {
		if f.ID == runID {
			out = append(out, f.Type)
		}
	}
	return out
}

// fakeSession is a controllable session. The test owns its out
// channel and manipulates outbound traffic / cancel observability.
type fakeSession struct {
	cancelCalls         int
	submitCalls         []permCall
	askCalls            []askCall
	submitErr           error
	askErr              error
	cancelMu, submitMu  sync.Mutex
	askMu               sync.Mutex
	closeOutOnCancel    bool
	out                 chan<- proto.Envelope
	closeOutOnCancelMu  sync.Once
	postCancelEnvelopes []proto.Envelope // emitted to out after Cancel fires
	ctx                 context.Context
}

type permCall struct {
	id       string
	decision proto.PermissionDecisionPayload
}

type askCall struct {
	id       string
	decision proto.PromptForUserChoiceDecisionPayload
}

func (s *fakeSession) CancellationOutcome() proto.DonePayload { return proto.DonePayload{} }

func (s *fakeSession) Cancel(context.Context) error {
	s.cancelMu.Lock()
	s.cancelCalls++
	s.cancelMu.Unlock()
	if s.closeOutOnCancel {
		s.closeOutOnCancelMu.Do(func() {
			for _, env := range s.postCancelEnvelopes {
				s.out <- env
			}
			close(s.out)
		})
	}
	return nil
}

func (s *fakeSession) SubmitPermission(_ context.Context, permID string, dec proto.PermissionDecisionPayload) error {
	s.submitMu.Lock()
	s.submitCalls = append(s.submitCalls, permCall{id: permID, decision: dec})
	s.submitMu.Unlock()
	return s.submitErr
}

func (s *fakeSession) SubmitPromptForUserChoice(_ context.Context, askID string, dec proto.PromptForUserChoiceDecisionPayload) error {
	s.askMu.Lock()
	s.askCalls = append(s.askCalls, askCall{id: askID, decision: dec})
	s.askMu.Unlock()
	return s.askErr
}

func (s *fakeSession) cancels() int {
	s.cancelMu.Lock()
	defer s.cancelMu.Unlock()
	return s.cancelCalls
}

func (s *fakeSession) submissions() []permCall {
	s.submitMu.Lock()
	defer s.submitMu.Unlock()
	out := make([]permCall, len(s.submitCalls))
	copy(out, s.submitCalls)
	return out
}

// ---------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------

// harness is a Router whose fake_alpha kind runs each Turn as a fakeSession.
type harness struct {
	router  *dispatch.Router
	sender  *recSender
	reg     *agent.Registry
	gotReq  chan proto.PromptRequestPayload
	gotSess chan *fakeSession
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{
		sender:  &recSender{},
		reg:     agent.NewRegistry(),
		gotReq:  make(chan proto.PromptRequestPayload, 16),
		gotSess: make(chan *fakeSession, 16),
	}
	registerSession(h.reg, proto.SupportedAgentKind{Kind: "fake_alpha", Available: true, Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{Permissions: proto.CapabilitySupported})}, func(ctx context.Context, req proto.PromptRequestPayload, out chan<- proto.Envelope) (agent.Session, error) {
		sess := &fakeSession{out: out, ctx: ctx, closeOutOnCancel: true}
		h.gotReq <- req
		h.gotSess <- sess
		return sess, nil
	})
	r, err := dispatch.New(dispatch.Config{Registry: h.reg, Sender: h.sender})
	if err != nil {
		t.Fatalf("dispatch.New: %v", err)
	}
	h.router = r
	return h
}

// registerSession declares info, without an Environment, and starts each
// Turn of the kind with factory.
func registerSession(reg *agent.Registry, info proto.SupportedAgentKind, factory agent.Factory) {
	info.Capabilities.EnvironmentNone = proto.CapabilitySupported
	reg.RegisterKind(info, prototest.ModelConfiguration(), factory)
	reg.RegisterExecutor(info.Kind, preparationExecutorFixture(func(_ context.Context, req proto.PromptRequestPayload) (preparedFixture, error) {
		return &controlledPreparation{start: func(ctx context.Context, id string, input proto.MessageInput, out chan<- proto.Envelope) (agent.Session, error) {
			req.RunID, req.Input = id, input
			return factory(ctx, req, out)
		}}, nil
	}))
}

// startRun binds the Session run to r, prepares its Executor of kind, starts
// run and waits until it accepts operations. sender records r's frames.
func startRun(t *testing.T, r *dispatch.Router, sender *recSender, kind, run string) {
	t.Helper()
	assign(t, r, run, "")
	prepare := scoped(t, run, proto.TypeExecutionPrepare, "prepare-"+run, noEnvironmentPreparation(run, proto.PromptRequestPayload{AgentKind: kind}))
	if err := r.Handle(t.Context(), prepare); err != nil {
		t.Fatalf("execution_prepare: %v", err)
	}
	ready := waitPreparationStatus(t, sender, prepare.ID, "ready", "")
	if err := r.Handle(t.Context(), scoped(t, run, proto.TypeExecutionStart, prepare.ID, proto.ExecutionStartPayload{Handle: ready.Handle, ExecutorID: ready.ExecutorID, RunID: run, Input: proto.TextInput("input")})); err != nil {
		t.Fatalf("execution_start: %v", err)
	}
	waitPreparationStatus(t, sender, prepare.ID, "started", "")
	waitFor(t, func() bool { return r.RunStartedForTest(run) }, "run "+run+" to accept operations")
}

// ref is the assignment the tests bind session to.
func ref(session string) proto.AssignmentRef {
	return proto.AssignmentRef{SessionID: session, AssignmentID: "assignment-" + session, Epoch: 1}
}

// assign binds session to r in environment.
func assign(t *testing.T, r *dispatch.Router, session, environment string) {
	t.Helper()
	if err := r.Handle(t.Context(), scoped(t, session, proto.TypeAssignmentBind, "bind-"+session, proto.AssignmentBindPayload{EnvironmentID: environment})); err != nil {
		t.Fatalf("assignment_bind: %v", err)
	}
}

// scoped is a frame of session's work under its assignment.
func scoped(t *testing.T, session, typ, id string, payload any) proto.Envelope {
	t.Helper()
	env, err := proto.NewEnvelope(typ, id, payload)
	if err != nil {
		t.Fatalf("NewEnvelope %s: %v", typ, err)
	}
	env.Assignment = ref(session)
	return env
}

// mustEnv is a frame of preparationSessionID's work, the Session most tests
// run.
func mustEnv(t *testing.T, typ, id string, payload any) proto.Envelope {
	t.Helper()
	return scoped(t, preparationSessionID, typ, id, payload)
}

// ---------------------------------------------------------------------
// tests
// ---------------------------------------------------------------------

func TestExecutionStartRunsInputAndForwardsOutput(t *testing.T) {
	h := newHarness(t)
	defer h.router.Shutdown(context.Background())

	startRun(t, h.router, h.sender, "fake_alpha", "run_1")
	req := <-h.gotReq
	sess := <-h.gotSess
	if req.RunID != "run_1" || len(req.Input) != 1 || *req.Input[0].Content[0].Text != "input" {
		t.Errorf("Turn got run %q input %+v, want run_1/input", req.RunID, req.Input)
	}

	// Session emits a delta + done; both should reach the sender.
	sess.out <- mustEnv(t, proto.TypeDelta, "run_1", proto.DeltaPayload{Delta: "hello", Sequence: 1})
	sess.out <- mustEnv(t, proto.TypeDone, "run_1", proto.DonePayload{Content: "hello"})

	waitForTypes(t, h.sender, "run_1", []string{proto.TypeDelta, proto.TypeDone})

	// Settlement should have removed the Run.
	waitFor(t, func() bool { return h.router.ActiveRuns() == 0 }, "active runs to drop to 0")
}

func TestExecutionStartRejectsDuplicateRunID(t *testing.T) {
	h := newHarness(t)
	defer h.router.Shutdown(context.Background())

	startRun(t, h.router, h.sender, "fake_alpha", "run_dup")
	<-h.gotReq

	// Another Executor must not start a second Turn for the same RunID.
	assign(t, h.router, "session-dup", "")
	second := executorAdmission(t, h.router, h.sender, "prepare-dup", noEnvironmentPreparation("session-dup", proto.PromptRequestPayload{AgentKind: "fake_alpha"}))
	start := scoped(t, "session-dup", proto.TypeExecutionStart, "prepare-dup", proto.ExecutionStartPayload{Handle: second.Handle, ExecutorID: second.ExecutorID, RunID: "run_dup", Input: proto.TextInput("input")})
	if err := h.router.Handle(context.Background(), start); err == nil {
		t.Fatal("duplicate Run started")
	}
	if status := waitPreparationStatus(t, h.sender, "prepare-dup", "rejected", ""); status.ErrorCode != "run_conflict" {
		t.Fatalf("duplicate start status = %+v, want run_conflict", status)
	}
	select {
	case extra := <-h.gotReq:
		t.Fatalf("Turn started twice for duplicate run, second run=%s", extra.RunID)
	default:
	}
}

func TestExecutionPrepareRejectsUnknownKind(t *testing.T) {
	h := newHarness(t)
	defer h.router.Shutdown(context.Background())

	assign(t, h.router, "x", "")
	env := scoped(t, "x", proto.TypeExecutionPrepare, "prepare_x", noEnvironmentPreparation("x", proto.PromptRequestPayload{AgentKind: "fake_beta"}))
	if err := h.router.Handle(context.Background(), env); err == nil {
		t.Error("unknown kind admitted")
	}
	if status := waitPreparationStatus(t, h.sender, "prepare_x", "rejected", ""); status.ErrorCode != "resource_unavailable" {
		t.Errorf("unknown kind status = %+v, want resource_unavailable", status)
	}
	if got, want := h.sender.typesFor("prepare_x"), []string{proto.TypePreparationStatus}; !slices.Equal(got, want) {
		t.Errorf("sender frames for prepare_x = %v, want %v", got, want)
	}
}

func TestExecutionStartRequiresRunID(t *testing.T) {
	h := newHarness(t)
	defer h.router.Shutdown(context.Background())

	assign(t, h.router, preparationSessionID, "")
	ready := executorAdmission(t, h.router, h.sender, "prepare", noEnvironmentPreparation(preparationSessionID, proto.PromptRequestPayload{AgentKind: "fake_alpha"}))
	start := mustEnv(t, proto.TypeExecutionStart, "prepare", proto.ExecutionStartPayload{Handle: ready.Handle, ExecutorID: ready.ExecutorID, Input: proto.TextInput("input")})
	if err := h.router.Handle(context.Background(), start); err == nil {
		t.Fatal("expected error on missing run id")
	}
	if status := waitPreparationStatus(t, h.sender, "prepare", "rejected", ""); status.ErrorCode != "invalid_start" {
		t.Fatalf("missing run id status = %+v, want invalid_start", status)
	}
}

func TestHandlePromptCancelInvokesSessionCancel(t *testing.T) {
	h := newHarness(t)
	defer h.router.Shutdown(context.Background())

	startRun(t, h.router, h.sender, "fake_alpha", "run_2")
	<-h.gotReq
	sess := <-h.gotSess

	if err := h.router.Handle(context.Background(), scoped(t, "run_2", proto.TypePromptCancel, "run_2", nil)); err != nil {
		t.Fatalf("prompt_cancel: %v", err)
	}

	waitFor(t, func() bool { return sess.cancels() == 1 }, "session.Cancel to fire once")
	waitFor(t, func() bool { return h.router.ActiveRuns() == 0 }, "session to be cleaned up")
}

func TestHandlePromptCancelUnknownRunIsNoop(t *testing.T) {
	h := newHarness(t)
	defer h.router.Shutdown(context.Background())

	if err := h.router.Handle(context.Background(), mustEnv(t, proto.TypePromptCancel, "ghost", nil)); err != nil {
		t.Errorf("cancel for unknown run = %v, want nil", err)
	}
}

func TestPermissionRequestIsIndexedAndDecisionRoutes(t *testing.T) {
	h := newHarness(t)
	defer h.router.Shutdown(context.Background())

	startRun(t, h.router, h.sender, "fake_alpha", "run_p")
	<-h.gotReq
	sess := <-h.gotSess

	// Session emits a permission_request; the Router indexes it before it forwards it.
	sess.out <- mustEnv(t, proto.TypePermissionRequest, "run_p", proto.PermissionRequestPayload{
		RequestID: "perm_abcd1234", Tool: "Bash", Title: "rm -rf /",
	})
	waitFor(t, func() bool { return hasFrame(h.sender, proto.TypePermissionRequest, "run_p") }, "permission_request to be forwarded")

	dec := scoped(t, "run_p", proto.TypePermissionDecision, "perm_abcd1234", proto.PermissionDecisionPayload{DeliveryID: "delivery-perm-1", Approved: true})
	if err := h.router.Handle(context.Background(), dec); err != nil {
		t.Fatalf("permission_decision: %v", err)
	}
	calls := sess.submissions()
	if len(calls) != 1 || calls[0].id != "perm_abcd1234" || !calls[0].decision.Approved {
		t.Errorf("submissions = %+v, want one approved perm_abcd1234", calls)
	}
	assertDecisionAck(t, h.sender, "delivery-perm-1", true, "")
	retry := scoped(t, "run_p", proto.TypePermissionDecision, "perm_abcd1234", proto.PermissionDecisionPayload{DeliveryID: "delivery-perm-2", Approved: true})
	if err := h.router.Handle(context.Background(), retry); err != nil {
		t.Fatalf("idempotent permission replay: %v", err)
	}
	if calls := sess.submissions(); len(calls) != 1 {
		t.Fatalf("idempotent replay reached agent twice: %+v", calls)
	}
	assertDecisionAck(t, h.sender, "delivery-perm-2", true, "")
	conflict := scoped(t, "run_p", proto.TypePermissionDecision, "perm_abcd1234", proto.PermissionDecisionPayload{DeliveryID: "delivery-perm-3", Approved: false})
	if err := h.router.Handle(context.Background(), conflict); err != nil {
		t.Fatalf("conflicting permission replay: %v", err)
	}
	assertDecisionAck(t, h.sender, "delivery-perm-3", false, "decision_conflict")
}

func TestPermissionDecisionUnknownPermIsNoop(t *testing.T) {
	h := newHarness(t)
	defer h.router.Shutdown(context.Background())

	if err := h.router.Handle(context.Background(), mustEnv(t, proto.TypePermissionDecision, "perm_unknown", proto.PermissionDecisionPayload{DeliveryID: "delivery-unknown-perm"})); err != nil {
		t.Errorf("decision for unknown perm = %v, want nil", err)
	}
	assertDecisionAck(t, h.sender, "delivery-unknown-perm", false, "not_pending")
}

func TestPromptForUserChoiceDecisionRoutesToSession(t *testing.T) {
	h := newHarness(t)
	defer h.router.Shutdown(context.Background())

	startRun(t, h.router, h.sender, "fake_alpha", "run_ask")
	<-h.gotReq
	sess := <-h.gotSess

	// Envelope.ID is the run id (server-side dispatch fans on it); the
	// ask id rides on the payload. Daemon's indexPermissionFrame reads
	// payload.AskID to seed askIndex.
	sess.out <- mustEnv(t, proto.TypePromptForUserChoice, "run_ask", proto.PromptForUserChoicePayload{
		AskID:     "ask_abcd1234",
		Questions: []proto.PromptForUserChoiceQuestion{{Question: "?", Options: []proto.PromptForUserChoiceOption{{Label: "yes"}, {Label: "no"}}}},
		ToolUseID: "toolu_42",
	})
	waitFor(t, func() bool { return hasFrame(h.sender, proto.TypePromptForUserChoice, "run_ask") }, "prompt_for_user_choice forwarded")

	dec := scoped(t, "run_ask", proto.TypePromptForUserChoiceDecision, "ask_abcd1234", proto.PromptForUserChoiceDecisionPayload{
		DeliveryID: "delivery-ask-1", QuestionAnswers: []proto.PromptForUserChoiceQuestionAnswer{{QuestionID: "q0", Answers: []string{"yes"}}},
	})
	if err := h.router.Handle(context.Background(), dec); err != nil {
		t.Fatalf("prompt_for_user_choice_decision: %v", err)
	}

	sess.askMu.Lock()
	calls := append([]askCall(nil), sess.askCalls...)
	sess.askMu.Unlock()
	if len(calls) != 1 || calls[0].id != "ask_abcd1234" {
		t.Fatalf("askCalls = %+v, want one ask_abcd1234", calls)
	}
	if len(calls[0].decision.QuestionAnswers[0].Answers) != 1 || calls[0].decision.QuestionAnswers[0].Answers[0] != "yes" {
		t.Errorf("answer payload mismatch: %+v", calls[0].decision)
	}
	assertDecisionAck(t, h.sender, "delivery-ask-1", true, "")

	// Cleanup contract: a successful decision drops the ask from both
	// the router-level index and the session's pendingAsks set, so a
	// stale retry short-circuits as "run gone".
	if got := h.router.AskIndexLenForTest(); got != 0 {
		t.Errorf("askIndex len = %d, want 0 after decision", got)
	}
	if got := h.router.PendingAsksLenForTest("run_ask"); got != 0 {
		t.Errorf("pendingAsks len = %d, want 0 after decision", got)
	}
}

// TestPromptForUserChoiceDecisionClearsIndexOnAgentUnknown locks in the
// cleanup branch: when the session returns ErrUnknownAsk (timer already
// consumed the entry), the router still drops the index so a retry doesn't
// loop into Submit again.
func TestPromptForUserChoiceDecisionClearsIndexOnAgentUnknown(t *testing.T) {
	h := newHarness(t)
	defer h.router.Shutdown(context.Background())

	startRun(t, h.router, h.sender, "fake_alpha", "run_ask_u")
	<-h.gotReq
	sess := <-h.gotSess
	sess.askErr = agent.ErrUnknownAsk

	sess.out <- mustEnv(t, proto.TypePromptForUserChoice, "run_ask_u", proto.PromptForUserChoicePayload{
		AskID:     "ask_xxxxxxxx",
		Questions: []proto.PromptForUserChoiceQuestion{{Question: "?", Options: []proto.PromptForUserChoiceOption{{Label: "yes"}}}},
		ToolUseID: "toolu_y",
	})
	waitFor(t, func() bool { return hasFrame(h.sender, proto.TypePromptForUserChoice, "run_ask_u") }, "prompt_for_user_choice forwarded")

	dec := scoped(t, "run_ask_u", proto.TypePromptForUserChoiceDecision, "ask_xxxxxxxx", proto.PromptForUserChoiceDecisionPayload{
		DeliveryID: "delivery-ask-gone", QuestionAnswers: []proto.PromptForUserChoiceQuestionAnswer{{QuestionID: "q0", Answers: []string{"yes"}}},
	})
	if err := h.router.Handle(context.Background(), dec); err != nil {
		t.Fatalf("prompt_for_user_choice_decision: %v", err)
	}

	if got := h.router.AskIndexLenForTest(); got != 0 {
		t.Errorf("askIndex len = %d, want 0 after ErrUnknownAsk", got)
	}
	if got := h.router.PendingAsksLenForTest("run_ask_u"); got != 0 {
		t.Errorf("pendingAsks len = %d, want 0 after ErrUnknownAsk", got)
	}
	assertDecisionAck(t, h.sender, "delivery-ask-gone", false, "not_pending")
}

func TestPromptForUserChoiceDecisionKeepsIndexOnTransientAgentError(t *testing.T) {
	h := newHarness(t)
	defer h.router.Shutdown(context.Background())

	startRun(t, h.router, h.sender, "fake_alpha", "run_ask_retry")
	<-h.gotReq
	sess := <-h.gotSess
	sess.askErr = errors.New("temporary stdin failure")

	sess.out <- mustEnv(t, proto.TypePromptForUserChoice, "run_ask_retry", proto.PromptForUserChoicePayload{
		AskID: "ask_retry", Questions: []proto.PromptForUserChoiceQuestion{{ID: "q0", Question: "Retry?"}},
	})
	waitFor(t, func() bool { return hasFrame(h.sender, proto.TypePromptForUserChoice, "run_ask_retry") }, "prompt_for_user_choice forwarded")

	decision := scoped(t, "run_ask_retry", proto.TypePromptForUserChoiceDecision, "ask_retry", proto.PromptForUserChoiceDecisionPayload{
		DeliveryID: "delivery-ask-retry", QuestionAnswers: []proto.PromptForUserChoiceQuestionAnswer{{QuestionID: "q0", Answers: []string{"yes"}}},
	})
	if err := h.router.Handle(context.Background(), decision); err != nil {
		t.Fatalf("first decision: %v", err)
	}
	assertDecisionAck(t, h.sender, "delivery-ask-retry", false, "runtime_error")
	if got := h.router.AskIndexLenForTest(); got != 1 {
		t.Fatalf("askIndex len = %d, want 1 after transient error", got)
	}
	if got := h.router.PendingAsksLenForTest("run_ask_retry"); got != 1 {
		t.Fatalf("pendingAsks len = %d, want 1 after transient error", got)
	}

	sess.askMu.Lock()
	sess.askErr = nil
	sess.askMu.Unlock()
	if err := h.router.Handle(context.Background(), decision); err != nil {
		t.Fatalf("retry decision: %v", err)
	}
	assertDecisionAck(t, h.sender, "delivery-ask-retry", true, "")
	if got := h.router.AskIndexLenForTest(); got != 0 {
		t.Fatalf("askIndex len = %d, want 0 after successful retry", got)
	}
}

func TestPromptForUserChoiceDecisionUnknownAskIsNoop(t *testing.T) {
	h := newHarness(t)
	defer h.router.Shutdown(context.Background())

	if err := h.router.Handle(context.Background(), mustEnv(t, proto.TypePromptForUserChoiceDecision, "ask_unknown", proto.PromptForUserChoiceDecisionPayload{DeliveryID: "delivery-unknown-ask"})); err != nil {
		t.Errorf("decision for unknown ask = %v, want nil", err)
	}
	assertDecisionAck(t, h.sender, "delivery-unknown-ask", false, "not_pending")
}

func assertDecisionAck(t *testing.T, sender *recSender, deliveryID string, applied bool, errorCode string) {
	t.Helper()
	frames := sender.snapshot()
	for index := len(frames) - 1; index >= 0; index-- {
		if frames[index].Type != proto.TypeInteractionDecisionAck {
			continue
		}
		var ack proto.InteractionDecisionAckPayload
		if err := frames[index].DecodePayload(&ack); err != nil {
			t.Fatalf("decode decision ack: %v", err)
		}
		if ack.DeliveryID == deliveryID {
			if ack.Applied != applied || ack.ErrorCode != errorCode {
				t.Fatalf("decision ack = %+v, want applied=%v error_code=%q", ack, applied, errorCode)
			}
			return
		}
	}
	t.Fatalf("no decision ack for delivery %q in %+v", deliveryID, frames)
}

func TestHandleAfterShutdownReturnsErrRouterClosed(t *testing.T) {
	h := newHarness(t)
	if err := h.router.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	err := h.router.Handle(context.Background(), mustEnv(t, proto.TypeExecutionPrepare, "r", proto.ExecutionPreparePayload{}))
	if !errors.Is(err, dispatch.ErrRouterClosed) {
		t.Errorf("post-shutdown Handle = %v, want ErrRouterClosed", err)
	}
}

func TestShutdownWaitsForPumpDrain(t *testing.T) {
	h := newHarness(t)

	startRun(t, h.router, h.sender, "fake_alpha", "rs")
	<-h.gotReq
	sess := <-h.gotSess
	sess.closeOutOnCancel = false

	// Background: emit one frame then close out shortly after
	// shutdown is asked for.
	go func() {
		sess.out <- mustEnv(t, proto.TypeDelta, "rs", proto.DeltaPayload{Delta: "x"})
		time.Sleep(20 * time.Millisecond)
		close(sess.out)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := h.router.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown returned %v before pump drained", err)
	}
	if h.router.ActiveRuns() != 0 {
		t.Errorf("ActiveRuns after Shutdown = %d, want 0", h.router.ActiveRuns())
	}
}

// ---------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------

func waitForTypes(t *testing.T, s *recSender, runID string, want []string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		got := s.typesFor(runID)
		if slices.Equal(got, want) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("never observed types %v for run %s; got %v", want, runID, s.typesFor(runID))
}

func waitFor(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", what)
}
