package cli

import (
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto/prototest"
	"github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig"
)

func TestHeartbeatNeverAdvertisesUndeclaredExport(t *testing.T) {
	local := proto.AgentKindCapabilities{LocalEnvironment: proto.CapabilitySupported, WorkspaceReadPreparation: proto.CapabilitySupported}
	exporting := local
	exporting.WorkspaceOutputExport = proto.CapabilitySupported
	registry := agent.NewRegistry()
	registry.RegisterKind(proto.SupportedAgentKind{Kind: "declared", Available: true, Capabilities: prototest.Capabilities(exporting)}, harnessconfig.Configuration{})
	registry.RegisterKind(proto.SupportedAgentKind{Kind: "undeclared", Available: true, Capabilities: prototest.Capabilities(local)}, harnessconfig.Configuration{})
	for _, canExport := range []bool{false, true} {
		for _, kind := range advertisedKinds(registry, canExport) {
			if got, want := kind.Capabilities.WorkspaceOutputExport.IsSupported(), canExport && kind.Kind == "declared"; got != want {
				t.Fatalf("%s with host export %v advertised export %v", kind.Kind, canExport, got)
			}
		}
	}
}
