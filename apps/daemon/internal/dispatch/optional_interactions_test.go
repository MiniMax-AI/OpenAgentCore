package dispatch_test

import (
	"context"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto/prototest"
)

// Wrapping Session proves that no responder stubs are required for a Session.
type lifecycleOnly struct{ agent.Session }

func TestOptionalInteractionResponders(t *testing.T) {
	for _, ask := range []bool{false, true} {
		t.Run(map[bool]string{false: "permission", true: "user choice"}[ask], func(t *testing.T) {
			h := newHarness(t)
			defer h.router.Shutdown(context.Background())
			var output chan<- proto.Envelope
			registerExecutorKind(h.reg, proto.SupportedAgentKind{Kind: "minimal", Available: true, Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{EnvironmentNone: proto.CapabilitySupported})}, sessionExecutor(func(_ context.Context, _ string, _ proto.MessageInput, out chan<- proto.Envelope) (agent.Session, error) {
				output = out
				s := &fakeSession{out: out, closeOutOnCancel: true}
				return lifecycleOnly{Session: s}, nil
			}))
			startRun(t, h.router, h.sender, "run", proto.PromptRequestPayload{AgentKind: "minimal"})
			event := mustEnv(t, proto.TypePermissionRequest, "run", proto.PermissionRequestPayload{RequestID: "interaction", Tool: "fixture"})
			decision := mustEnv(t, proto.TypePermissionDecision, "interaction", proto.PermissionDecisionPayload{DeliveryID: "decision", Approved: true})
			if ask {
				event = mustEnv(t, proto.TypePromptForUserChoice, "run", proto.PromptForUserChoicePayload{AskID: "interaction"})
				decision = mustEnv(t, proto.TypePromptForUserChoiceDecision, "interaction", proto.PromptForUserChoiceDecisionPayload{DeliveryID: "decision"})
			}
			// An inconsistent adapter emitted an interaction it cannot answer: reject it,
			// never acknowledge application or call a fabricated responder.
			output <- event
			waitFor(t, func() bool { return hasFrame(h.sender, event.Type, "run") }, "interaction indexed")
			if err := h.router.Handle(t.Context(), decision); err != nil {
				t.Fatal(err)
			}
			assertDecisionAck(t, h.sender, "decision", false, "unsupported")
		})
	}
}
