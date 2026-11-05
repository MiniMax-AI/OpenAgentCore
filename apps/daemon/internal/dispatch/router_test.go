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

// recSender records every Envelope. failNow makes the next Send fail
// (used to exercise the pump's error path).
type recSender struct {
	mu      sync.Mutex
	frames  []proto.Envelope
	failNow bool
}

func (s *recSender) Send(_ context.Context, env proto.Envelope) error {
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
	runID               string
	input               proto.MessageInput
	cancelCalls         int
	cancelMu            sync.Mutex
	closeOutOnCancel    bool
	out                 chan<- proto.Envelope
	closeOutOnCancelMu  sync.Once
	postCancelEnvelopes []proto.Envelope // emitted to out after Cancel fires
	ctx                 context.Context
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

func (s *fakeSession) cancels() int {
	s.cancelMu.Lock()
	defer s.cancelMu.Unlock()
	return s.cancelCalls
}

// ---------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------

// harness is a Router whose fake_alpha kind starts a recorded fakeSession
// for each Turn.
type harness struct {
	router  *dispatch.Router
	sender  *recSender
	reg     *agent.Registry
	gotSess chan *fakeSession
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{
		sender:  &recSender{},
		reg:     agent.NewRegistry(),
		gotSess: make(chan *fakeSession, 16),
	}
	registerExecutorKind(h.reg, proto.SupportedAgentKind{Kind: "fake_alpha", Available: true, Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{EnvironmentNone: proto.CapabilitySupported})}, sessionExecutor(func(_ context.Context, runID string, input proto.MessageInput, out chan<- proto.Envelope) (agent.Session, error) {
		sess := &fakeSession{runID: runID, input: input, out: out}
		h.gotSess <- sess
		return sess, nil
	}))
	r, err := dispatch.New(dispatch.Config{Registry: h.reg, Sender: h.sender})
	if err != nil {
		t.Fatalf("dispatch.New: %v", err)
	}
	h.router = r
	return h
}

// sessionExecutor runs each Turn through start on a disposable Executor.
func sessionExecutor(start func(context.Context, string, proto.MessageInput, chan<- proto.Envelope) (agent.Session, error)) agent.ExecutorFactory {
	return preparationExecutorFixture(func(context.Context, proto.PromptRequestPayload) (preparedFixture, error) {
		return &controlledPreparation{start: start}, nil
	})
}

// startRun prepares a dedicated Executor for run, starts the Run with input
// "input" and waits until it accepts operations.
func startRun(t *testing.T, r *dispatch.Router, s *recSender, run string, config proto.PromptRequestPayload) {
	t.Helper()
	ready := executorAdmission(t, r, s, "prepare-"+run, noEnvironmentPreparation("session-"+run, config))
	startExecutorTurn(t, r, s, "prepare-"+run, run, ready)
	waitFor(t, func() bool { return r.RunStartedForTest(run) }, "run "+run+" to accept operations")
}

func mustEnv(t *testing.T, typ, id string, payload any) proto.Envelope {
	t.Helper()
	env, err := proto.NewEnvelope(typ, id, payload)
	if err != nil {
		t.Fatalf("NewEnvelope %s: %v", typ, err)
	}
	return env
}

// ---------------------------------------------------------------------
// tests
// ---------------------------------------------------------------------

func TestExecutionStartRunsInputAndForwardsOutput(t *testing.T) {
	h := newHarness(t)
	defer h.router.Shutdown(context.Background())

	startRun(t, h.router, h.sender, "run_1", proto.PromptRequestPayload{AgentKind: "fake_alpha"})
	sess := <-h.gotSess
	if sess.runID != "run_1" || len(sess.input) != 1 || *sess.input[0].Content[0].Text != "input" {
		t.Errorf("Turn got run %q input %+v, want run_1/input", sess.runID, sess.input)
	}

	// Session emits a delta + done; both should reach the sender.
	sess.out <- mustEnv(t, proto.TypeDelta, "run_1", proto.DeltaPayload{Delta: "hello", Sequence: 1})
	sess.out <- mustEnv(t, proto.TypeDone, "run_1", proto.DonePayload{Content: "hello"})
	close(sess.out)

	waitForTypes(t, h.sender, "run_1", []string{proto.TypeDelta, proto.TypeDone})

	// Settlement should have removed the Run.
	waitFor(t, func() bool { return h.router.ActiveRuns() == 0 }, "active runs to drop to 0")
}

func TestExecutionStartRejectsDuplicateRunID(t *testing.T) {
	h := newHarness(t)
	defer h.router.Shutdown(context.Background())

	startRun(t, h.router, h.sender, "run_dup", proto.PromptRequestPayload{AgentKind: "fake_alpha"})
	sess := <-h.gotSess

	// Another Executor must not start a second Turn for the same RunID.
	second := executorAdmission(t, h.router, h.sender, "prepare-dup", noEnvironmentPreparation("session-dup", proto.PromptRequestPayload{AgentKind: "fake_alpha"}))
	start := mustEnv(t, proto.TypeExecutionStart, "prepare-dup", proto.ExecutionStartPayload{Handle: second.Handle, ExecutorID: second.ExecutorID, RunID: "run_dup", Input: proto.TextInput("input")})
	if err := h.router.Handle(context.Background(), start); err == nil {
		t.Fatal("duplicate Run started")
	}
	if status := waitPreparationStatus(t, h.sender, "prepare-dup", "rejected", ""); status.ErrorCode != "run_conflict" {
		t.Fatalf("duplicate start status = %+v, want run_conflict", status)
	}

	select {
	case extra := <-h.gotSess:
		t.Fatalf("Turn started twice for duplicate run, second run=%s", extra.runID)
	case <-time.After(50 * time.Millisecond):
	}
	close(sess.out)
}

func TestExecutionPrepareRejectsUnknownKind(t *testing.T) {
	h := newHarness(t)
	defer h.router.Shutdown(context.Background())

	env := mustEnv(t, proto.TypeExecutionPrepare, "prepare_x", noEnvironmentPreparation("x", proto.PromptRequestPayload{AgentKind: "fake_beta"}))
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

	ready := executorAdmission(t, h.router, h.sender, "prepare", noEnvironmentPreparation("session", proto.PromptRequestPayload{AgentKind: "fake_alpha"}))
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

	startRun(t, h.router, h.sender, "run_2", proto.PromptRequestPayload{AgentKind: "fake_alpha"})
	sess := <-h.gotSess
	sess.closeOutOnCancel = true

	if err := h.router.Handle(context.Background(), mustEnv(t, proto.TypePromptCancel, "run_2", nil)); err != nil {
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

func TestHandleUnknownTypeIsNoop(t *testing.T) {
	h := newHarness(t)
	defer h.router.Shutdown(context.Background())

	if err := h.router.Handle(context.Background(), proto.Envelope{Type: "fancy_new_event"}); err != nil {
		t.Errorf("unknown type Handle = %v, want nil", err)
	}
}

func TestHandleAfterShutdownReturnsErrRouterClosed(t *testing.T) {
	h := newHarness(t)
	if err := h.router.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	err := h.router.Handle(context.Background(), mustEnv(t, proto.TypeExecutionPrepare, "r", noEnvironmentPreparation("session", proto.PromptRequestPayload{AgentKind: "fake_alpha"})))
	if !errors.Is(err, dispatch.ErrRouterClosed) {
		t.Errorf("post-shutdown Handle = %v, want ErrRouterClosed", err)
	}
}

func TestShutdownWaitsForPumpDrain(t *testing.T) {
	h := newHarness(t)

	startRun(t, h.router, h.sender, "rs", proto.PromptRequestPayload{AgentKind: "fake_alpha"})
	sess := <-h.gotSess

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
