package dispatch_test

import (
	"context"
	"errors"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/dispatch"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto/prototest"
	"testing"
	"time"
)

type shutdownAllSendsBlockSender struct {
	recSender
	entered  chan struct{}
	terminal chan context.Context
	rescue   chan struct{}
}

func (s *shutdownAllSendsBlockSender) Send(ctx context.Context, env proto.Envelope) error {
	if env.Type == proto.TypePromptSteerAck {
		close(s.entered)
		select {
		case <-ctx.Done():
			<-time.After(50 * time.Millisecond)
			return ctx.Err()
		case <-s.rescue:
			return errors.New("test cleanup")
		}
	}
	if env.Type == proto.TypeError {
		s.terminal <- ctx
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-s.rescue:
			return errors.New("test cleanup")
		}
	}
	return s.recSender.Send(ctx, env)
}

func TestShutdownCancelsCompletionErrorSend(t *testing.T) {
	sender := &shutdownAllSendsBlockSender{entered: make(chan struct{}), terminal: make(chan context.Context, 1), rescue: make(chan struct{})}
	registry := agent.NewRegistry()
	var session *fakeSession
	registerSession(registry, proto.SupportedAgentKind{Kind: "codex", Available: true, Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{Steering: proto.CapabilitySupported, DurableInputReceipts: proto.CapabilitySupported})}, func(_ context.Context, _ proto.PromptRequestPayload, out chan<- proto.Envelope) (agent.Session, error) {
		session = &fakeSession{out: out, closeOutOnCancel: true}
		return &steeringSession{fakeSession: session, steer: func(context.Context, proto.PromptSteerPayload) error { return nil }}, nil
	})
	router, err := dispatch.New(dispatch.Config{Registry: registry, Sender: sender})
	if err != nil {
		t.Fatal(err)
	}
	startRun(t, router, &sender.recSender, "codex", "shutdown-terminal")
	if err = router.Handle(context.Background(), scoped(t, "shutdown-terminal", proto.TypePromptSteer, "shutdown-terminal", proto.PromptSteerPayload{InputID: "one", Input: proto.TextInput("text")})); err != nil {
		t.Fatal(err)
	}
	<-sender.entered
	session.out <- mustEnv(t, proto.TypeDone, "shutdown-terminal", proto.DonePayload{})
	waitFor(t, func() bool { return router.SteeringClosedForTest("shutdown-terminal") }, "receipt join")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	shutdownErr := router.Shutdown(ctx)
	active := router.ActiveRuns()
	select {
	case terminalCtx := <-sender.terminal:
		t.Logf("error send context: Done is nil=%t, Err=%v", terminalCtx.Done() == nil, terminalCtx.Err())
	default:
	}
	close(sender.rescue)
	waitFor(t, func() bool { return router.ActiveRuns() == 0 }, "test cleanup")
	if shutdownErr != nil || active != 0 {
		t.Fatalf("cooperating receipt sender canceled, but shutdown failed: err=%v active_runs=%d", shutdownErr, active)
	}
}
