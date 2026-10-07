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

func TestSteeringUsesAdmittedDeclarationAndDoesNotReplayUnsupportedImplementation(t *testing.T) {
	for _, supported := range []bool{false, true} {
		t.Run(fmt.Sprint(supported), func(t *testing.T) {
			h := newHarness(t)
			defer h.router.Shutdown(context.Background())
			var calls atomic.Int32
			factory := func(_ context.Context, _ proto.PromptRequestPayload, out chan<- proto.Envelope) (agent.Session, error) {
				return &steeringSession{fakeSession: &fakeSession{out: out, closeOutOnCancel: true}, steer: func(context.Context, proto.PromptSteerPayload) error {
					calls.Add(1)
					return fmt.Errorf("%w: fixture has no active input", agent.ErrUnsupportedOperation)
				}}, nil
			}
			info := proto.SupportedAgentKind{Kind: "fixture", Available: true, Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{Steering: proto.CapabilityFromBool(supported)})}
			registerSession(h.reg, info, factory)
			startRun(t, h.router, h.sender, "fixture", "run")
			// A new registration cannot rewrite the already admitted owner's contract.
			info.Capabilities.Steering = proto.CapabilityFromBool(!supported)
			registerSession(h.reg, info, factory)
			for range 2 {
				if err := handleSteeringAndWait(t, h, scoped(t, "run", proto.TypePromptSteer, "run", proto.PromptSteerPayload{InputID: "input", Input: proto.TextInput("hello")})); err != nil {
					t.Fatal(err)
				}
				ack := lastSteeringAck(t, h.sender, "run", "input")
				code := "unsupported"
				if supported {
					code = "contract_violation"
				}
				if ack.Accepted || ack.Written || ack.ErrorCode != code {
					t.Fatal(ack)
				}
			}
			want := int32(0)
			if supported {
				want = 1
			}
			if calls.Load() != want {
				t.Fatalf("native calls %d, want %d", calls.Load(), want)
			}
		})
	}
}
