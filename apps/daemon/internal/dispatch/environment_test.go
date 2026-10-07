package dispatch_test

import (
	"context"
	"errors"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto/prototest"
)

func TestLocalEnvironmentRequiresAvailableCapability(t *testing.T) {
	for _, mode := range []string{"unsupported", "unavailable", "none conflict", "supported"} {
		t.Run(mode, func(t *testing.T) {
			h := localPreparationHarness(t)
			defer h.router.Shutdown(context.Background())
			called := false
			h.reg.RegisterKind(proto.SupportedAgentKind{Kind: "codex", Available: mode != "unavailable",
				Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{LocalEnvironment: proto.CapabilityFromBool(mode != "unsupported"), EnvironmentNone: proto.CapabilitySupported})},
				prototest.ModelConfiguration(), func(context.Context, proto.PromptRequestPayload, chan<- proto.Envelope) (agent.Session, error) {
					return nil, errors.New("ordinary factory is forbidden")
				})
			h.reg.RegisterExecutor("codex", func(_ context.Context, req proto.PromptRequestPayload) (agent.Executor, error) {
				called = true
				if req.LocalEnvironment == nil || req.LocalEnvironment.ID != preparationEnvironmentID {
					t.Error("local descriptor lost before factory")
				}
				return nil, errors.New("controlled factory stop")
			})
			req := preparationRequest()
			req.Configuration.AgentKind = "codex"
			req.Configuration.DisableExecutionEnvironment = mode == "none conflict"
			_ = h.router.Handle(t.Context(), mustEnv(t, proto.TypeExecutionPrepare, "local", req))
			state := "rejected"
			if mode == "supported" {
				state = "failed"
			}
			waitPreparationStatus(t, h.sender, "local", state, "")
			if called != (mode == "supported") {
				t.Fatalf("unexpected factory call for %s", mode)
			}
		})
	}
}
