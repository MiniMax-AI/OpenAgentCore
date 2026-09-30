package cli

import "github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig"

import (
	"context"
	"errors"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/authoring"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto/prototest"
)

func TestPreparationRegistrationBypassesProductWrappers(t *testing.T) {
	for _, supported := range []bool{false, true} {
		reg := agent.NewRegistry()
		registerAgentKinds(reg, agentCLIDiscovery{Codex: proto.SupportedAgentKind{Kind: "codex", Available: true, Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{LocalEnvironment: proto.CapabilityFromBool(supported)})}, ClaudeCode: proto.SupportedAgentKind{Kind: "claude_code", Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{})}, OpenCode: proto.SupportedAgentKind{Kind: "opencode", Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{})}, Pi: proto.SupportedAgentKind{Kind: "pi", Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{})}, MCode: proto.SupportedAgentKind{Kind: "mcode", Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{})}}, "http://unreachable.invalid")
		_, err := reg.ResolvePreparation("codex")
		if (err == nil) != supported {
			t.Fatal("unverified native version advertised preparation")
		}
		if !supported {
			continue
		}
		stop := errors.New("controlled preparation stop")
		reg.RegisterPreparation("codex", true, func(_ context.Context, req proto.PromptRequestPayload) (agent.Prepared, error) {
			if req.RunID != "" || req.WorkspaceAuthoring || len(req.AgentOptions) != 0 {
				t.Error("product wrapper injected preparation context")
			}
			return nil, stop
		})
		wrapped := authoringRegistry(reg, authoring.New(nil))
		prepare, err := wrapped.ResolvePreparation("codex")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := prepare(t.Context(), proto.PromptRequestPayload{AgentKind: "codex"}); !errors.Is(err, stop) {
			t.Fatal("raw preparation was lost or wrapped", err)
		}
		for _, info := range wrapped.SupportedAgentKinds() {
			if info.Kind == "codex" && (!info.Capabilities.Preparation.IsSupported() || !info.Capabilities.WorkspaceReadPreparation.IsSupported()) {
				t.Fatal("real heartbeat registry lost preparation")
			}
		}
		wrapped.RegisterKind(proto.SupportedAgentKind{Kind: "codex", Available: true, Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{})}, harnessconfig.Configuration{}, func(context.Context, proto.PromptRequestPayload, chan<- proto.Envelope) (agent.Session, error) {
			return nil, stop
		})
		if _, err := wrapped.ResolvePreparation("codex"); err == nil {
			t.Fatal("factory replacement retained stale preparation")
		}
	}
}
