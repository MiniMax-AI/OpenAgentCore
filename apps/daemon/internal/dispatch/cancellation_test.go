package dispatch_test

import "github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig"

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

func TestCompletionWaitsForNativeWriterRelease(t *testing.T) {
	h := newHarness(t)
	defer h.router.Shutdown(context.Background())
	sess := &cancelReceiptSession{entered: make(chan struct{}), release: make(chan struct{})}
	h.reg.RegisterKind(proto.SupportedAgentKind{Kind: "codex", Available: true, Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{})}, harnessconfig.Configuration{}, func(ctx context.Context, req proto.PromptRequestPayload, out chan<- proto.Envelope) (agent.Session, error) {
		sess.fakeSession = &fakeSession{out: out, closeOutOnCancel: true}
		return sess, nil
	})
	if err := h.router.Handle(context.Background(), mustEnv(t, proto.TypePromptRequest, "release", proto.PromptRequestPayload{AgentKind: "codex", AgentStateKey: "stable", ReleaseOnCompletion: true})); err != nil {
		t.Fatal(err)
	}
	sess.out <- mustEnv(t, proto.TypeDone, "release", proto.DonePayload{Content: "Finished"})
	<-sess.entered
	if len(h.sender.snapshot()) != 0 {
		t.Fatal("completion acknowledged before native writer was released")
	}
	close(sess.release)
	waitFor(t, func() bool { return h.router.ActiveRuns() == 0 }, "release completion")
	frames := h.sender.snapshot()
	if len(frames) != 1 || frames[0].Type != proto.TypeDone || sess.cancels() != 1 {
		t.Fatal("completion or native release missing")
	}
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
			h.reg.RegisterKind(proto.SupportedAgentKind{Kind: "codex", Available: true, Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{})}, harnessconfig.Configuration{}, func(ctx context.Context, req proto.PromptRequestPayload, out chan<- proto.Envelope) (agent.Session, error) {
				sess.fakeSession = &fakeSession{out: out, closeOutOnCancel: true}
				return sess, nil
			})
			if err := h.router.Handle(context.Background(), mustEnv(t, proto.TypePromptRequest, "run", proto.PromptRequestPayload{AgentKind: "codex"})); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				done <- h.router.Handle(context.Background(), mustEnv(t, proto.TypePromptCancel, "run", proto.PromptCancelPayload{DeliveryID: "cancel-1"}))
			}()
			<-sess.entered
			for _, env := range h.sender.snapshot() {
				if env.Type == proto.TypeInteractionDecisionAck {
					t.Fatal("cancellation acknowledged before adapter returned")
				}
			}
			close(sess.release)
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			found := false
			for _, env := range h.sender.snapshot() {
				if env.Type == proto.TypeInteractionDecisionAck {
					found = true
					var ack proto.InteractionDecisionAckPayload
					_ = env.DecodePayload(&ack)
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
				}
			}
			if !found {
				t.Fatal("missing cancellation receipt")
			}
		})
	}
}

func TestLegacyCancellationDoesNotEmitNewFrames(t *testing.T) {
	h := newHarness(t)
	defer h.router.Shutdown(context.Background())
	if err := h.router.Handle(context.Background(), mustEnv(t, proto.TypePromptRequest, "legacy", proto.PromptRequestPayload{AgentKind: "fake_alpha"})); err != nil {
		t.Fatal(err)
	}
	sess := <-h.gotSess
	sess.closeOutOnCancel = true
	if err := h.router.Handle(context.Background(), mustEnv(t, proto.TypePromptCancel, "legacy", proto.PromptCancelPayload{})); err != nil {
		t.Fatal(err)
	}
	for _, env := range h.sender.snapshot() {
		if env.Type == proto.TypeInteractionDecisionAck {
			t.Fatal("legacy cancellation emitted new receipt")
		}
	}
}
