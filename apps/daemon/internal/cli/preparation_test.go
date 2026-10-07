package cli

import (
	"context"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto/prototest"
	"github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig"
)

func TestPreparationRegistrationFollowsNativeSupport(t *testing.T) {
	for _, supported := range []bool{false, true} {
		reg := agent.NewRegistry()
		info := proto.SupportedAgentKind{Kind: "codex", Available: true, Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{LocalEnvironment: proto.CapabilityFromBool(supported)})}
		runtime := agent.Runtime{Info: info}
		if supported {
			runtime.Preparation = func(context.Context, proto.PromptRequestPayload) (agent.Prepared, error) { return nil, nil }
			runtime.WorkspaceReadPreparation = true
		}
		registerAgentKinds(reg, agentCLIDiscovery{{declaration: agent.Declaration{Info: info}, runtime: runtime}})

		_, err := reg.ResolvePreparation("codex")
		if (err == nil) != supported {
			t.Fatal("unverified native version advertised preparation")
		}
		if !supported {
			continue
		}
		for _, info := range reg.SupportedAgentKinds() {
			if info.Kind == "codex" && (!info.Capabilities.Preparation.IsSupported() || !info.Capabilities.WorkspaceReadPreparation.IsSupported()) {
				t.Fatal("heartbeat registry lost preparation")
			}
		}
		reg.RegisterKind(proto.SupportedAgentKind{Kind: "codex", Available: true, Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{})}, harnessconfig.Configuration{})
		if _, err := reg.ResolvePreparation("codex"); err == nil {
			t.Fatal("kind replacement retained stale preparation")
		}
	}
}
