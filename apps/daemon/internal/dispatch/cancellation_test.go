package dispatch_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto/prototest"
)

type cancelReceiptSession struct {
	*fakeSession
	entered chan struct{}
	release chan struct{}
	err     error
	outcome proto.DonePayload
}

func (s *cancelReceiptSession) CancellationOutcome() proto.DonePayload {
	return s.outcome
}

func (s *cancelReceiptSession) Cancel(ctx context.Context) error {
	if s.entered != nil {
		close(s.entered)
		<-s.release
		s.entered = nil
	}
	_ = s.fakeSession.Cancel(ctx)
	return s.err
}

func registerCancelReceiptKind(h *harness, sess *cancelReceiptSession) {
	registerSession(h.reg, proto.SupportedAgentKind{Kind: "codex", Available: true, Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{})}, func(_ context.Context, _ proto.PromptRequestPayload, out chan<- proto.Envelope) (agent.Session, error) {
		sess.fakeSession = &fakeSession{out: out, closeOutOnCancel: true}
		return sess, nil
	})
}

func TestCompletionWaitsForNativeWriterRelease(t *testing.T) {
	h := newHarness(t)
	defer h.router.Shutdown(context.Background())
	sess := &cancelReceiptSession{entered: make(chan struct{}), release: make(chan struct{})}
	registerCancelReceiptKind(h, sess)
	startRun(t, h.router, h.sender, "codex", "release")
	sess.out <- mustEnv(t, proto.TypeDone, "release", proto.DonePayload{Content: "Finished"})
	<-sess.entered
	if len(h.sender.typesFor("release")) != 0 {
		t.Fatal("completion acknowledged before native writer was released")
	}
	close(sess.release)
	waitFor(t, func() bool { return hasFrame(h.sender, proto.TypeDone, "release") }, "release completion")
	if frames := h.sender.typesFor("release"); len(frames) != 1 || frames[0] != proto.TypeDone || sess.cancels() != 1 {
		t.Fatal("completion or native release missing")
	}
}

func TestCancellationReceiptFollowsAdapterOutcome(t *testing.T) {
	observed := proto.DonePayload{Content: "partial output", Metadata: map[string]any{proto.DoneMetaAgentSessionID: "native-cancelled"}}
	for _, test := range []struct {
		name    string
		outcome proto.DonePayload
		err     error
	}{
		{name: "observed", outcome: observed},
		{name: "unknown"},
		{name: "failed", outcome: observed, err: errors.New("adapter could not cancel")},
		{name: "unsupported", outcome: observed, err: agent.ErrUnsupportedOperation},
		{name: "deadline", outcome: observed, err: context.DeadlineExceeded},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newHarness(t)
			defer h.router.Shutdown(context.Background())
			sess := &cancelReceiptSession{entered: make(chan struct{}), release: make(chan struct{}), outcome: test.outcome, err: test.err}
			registerCancelReceiptKind(h, sess)
			startRun(t, h.router, h.sender, "codex", "run")
			if err := h.router.Handle(context.Background(), scoped(t, "run", proto.TypePromptCancel, "run", proto.PromptCancelPayload{DeliveryID: "cancel-1"})); err != nil {
				t.Fatal(err)
			}
			<-sess.entered
			if len(cancellationAcks(h.sender)) != 0 {
				t.Fatal("cancellation acknowledged before adapter returned")
			}
			close(sess.release)
			waitFor(t, func() bool { return len(cancellationAcks(h.sender)) != 0 }, "cancellation receipt")
			acks := cancellationAcks(h.sender)
			if len(acks) != 1 {
				t.Fatalf("cancellation receipts: %+v", acks)
			}
			ack := acks[0]
			if ack.Applied != (test.err == nil) || ack.DeliveryID != "cancel-1" {
				t.Fatalf("wrong receipt: %+v", ack)
			}
			if test.err == nil {
				if ack.ErrorCode != "" || ack.Outcome == nil || !reflect.DeepEqual(*ack.Outcome, test.outcome) {
					t.Fatalf("cancellation receipt changed observed evidence: %+v", ack)
				}
			} else if ack.ErrorCode != "cancel_failed" || ack.Outcome != nil {
				t.Fatalf("failed cancellation supplied a success outcome: %+v", ack)
			}
		})
	}
}

func TestLegacyCancellationDoesNotEmitNewFrames(t *testing.T) {
	h := newHarness(t)
	defer h.router.Shutdown(context.Background())
	startRun(t, h.router, h.sender, "fake_alpha", "legacy")
	if err := h.router.Handle(context.Background(), scoped(t, "legacy", proto.TypePromptCancel, "legacy", proto.PromptCancelPayload{})); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return hasFrame(h.sender, proto.TypeDone, "legacy") }, "cancellation settlement")
	for _, env := range h.sender.snapshot() {
		if env.Type == proto.TypeInteractionDecisionAck {
			t.Fatal("legacy cancellation emitted new receipt")
		}
	}
}
