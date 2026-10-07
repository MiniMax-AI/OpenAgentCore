package dispatch_test

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto/prototest"
)

func TestSteeringDoesNotReplayUnsupportedImplementation(t *testing.T) {
	h := newHarness(t)
	defer h.router.Shutdown(context.Background())
	var calls atomic.Int32
	factory := func(_ context.Context, _ proto.PromptRequestPayload, out chan<- proto.Envelope) (agent.Session, error) {
		return &steeringSession{fakeSession: &fakeSession{out: out, closeOutOnCancel: true}, steer: func(context.Context, proto.PromptSteerPayload) error {
			calls.Add(1)
			return fmt.Errorf("%w: fixture has no active input", agent.ErrUnsupportedOperation)
		}}, nil
	}
	registerSession(h.reg, proto.SupportedAgentKind{Kind: "fixture", Available: true, Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{})}, factory)
	startRun(t, h.router, h.sender, "fixture", "run")
	for range 2 {
		if err := handleSteeringAndWait(t, h, scoped(t, "run", proto.TypePromptSteer, "run", proto.PromptSteerPayload{InputID: "input", Input: proto.TextInput("hello")})); err != nil {
			t.Fatal(err)
		}
		if ack := lastSteeringAck(t, h.sender, "run", "input"); ack.Accepted || ack.Written || ack.ErrorCode != "contract_violation" {
			t.Fatal(ack)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("native calls %d, want 1", calls.Load())
	}
}
