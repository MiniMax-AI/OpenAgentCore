package dispatch_test

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto/prototest"
	"github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig"
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
			h.reg.RegisterKind(info, harnessconfig.Configuration{}, factory)
			if err := h.router.Handle(t.Context(), mustEnv(t, proto.TypePromptRequest, "run", proto.PromptRequestPayload{AgentKind: "fixture"})); err != nil {
				t.Fatal(err)
			}
			// A new registration cannot rewrite the already admitted owner's contract.
			info.Capabilities.Steering = proto.CapabilityFromBool(!supported)
			h.reg.RegisterKind(info, harnessconfig.Configuration{}, factory)
			for range 2 {
				if err := handleSteeringAndWait(t, h, mustEnv(t, proto.TypePromptSteer, "run", proto.PromptSteerPayload{InputID: "input", Input: proto.TextInput("hello")})); err != nil {
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

func TestInteractionDeclarationPrecedesResponderMethods(t *testing.T) {
	for _, supported := range []bool{false, true} {
		for _, ask := range []bool{false, true} {
			t.Run(fmt.Sprintf("supported=%v/ask=%v", supported, ask), func(t *testing.T) {
				h := newHarness(t)
				defer h.router.Shutdown(context.Background())
				var session *fakeSession
				info := proto.SupportedAgentKind{Kind: "fixture", Available: true, Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{Permissions: proto.CapabilityFromBool(supported)})}
				h.reg.RegisterKind(info, harnessconfig.Configuration{}, func(_ context.Context, _ proto.PromptRequestPayload, out chan<- proto.Envelope) (agent.Session, error) {
					session = &fakeSession{out: out, closeOutOnCancel: true, submitErr: agent.ErrUnsupportedOperation, askErr: agent.ErrUnsupportedOperation}
					return session, nil
				})
				if err := h.router.Handle(t.Context(), mustEnv(t, proto.TypePromptRequest, "run", proto.PromptRequestPayload{AgentKind: "fixture"})); err != nil {
					t.Fatal(err)
				}
				event := mustEnv(t, proto.TypePermissionRequest, "run", proto.PermissionRequestPayload{RequestID: "interaction", Tool: "fixture"})
				decision := mustEnv(t, proto.TypePermissionDecision, "interaction", proto.PermissionDecisionPayload{DeliveryID: "decision", Approved: true})
				if ask {
					event = mustEnv(t, proto.TypePromptForUserChoice, "run", proto.PromptForUserChoicePayload{AskID: "interaction"})
					decision = mustEnv(t, proto.TypePromptForUserChoiceDecision, "interaction", proto.PromptForUserChoiceDecisionPayload{DeliveryID: "decision"})
				}
				session.out <- event
				waitFor(t, func() bool { return len(h.sender.snapshot()) > 0 }, "interaction indexed")
				if err := h.router.Handle(t.Context(), decision); err != nil {
					t.Fatal(err)
				}
				code := "unsupported"
				if supported {
					code = "contract_violation"
				}
				assertDecisionAck(t, h.sender, "decision", false, code)
				count := len(session.submissions())
				if ask {
					session.askMu.Lock()
					count = len(session.askCalls)
					session.askMu.Unlock()
				}
				want := 0
				if supported {
					want = 1
				}
				if count != want {
					t.Fatalf("native calls %d, want %d", count, want)
				}
			})
		}
	}
}
