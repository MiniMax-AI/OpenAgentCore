package localworkspace

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentcapabilities"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

func TestCapabilityPathsStayRuntimeOwnedAndDoNotGateReads(t *testing.T) {
	binding, request := testBinding(t)
	request.LocalEnvironment.CapabilityRoot = "/caller/installation"
	request.LocalEnvironment.Skills = []agentcapabilities.InstalledSkill{{RelativeRoot: "caller/private", PackageRoot: "caller", InstallationRoot: "/caller/installation"}}
	secret := "private-mcp-marker"
	request.LocalEnvironment.MCP = []proto.EnvironmentMCP{{PackageRoot: "caller/private", BearerToken: &secret}}
	raw, err := json.Marshal(request.LocalEnvironment)
	if err != nil || bytes.Contains(raw, []byte("caller")) || bytes.Contains(raw, []byte("skills")) || bytes.Contains(raw, []byte(secret)) {
		t.Fatal("Runtime paths crossed the public daemon descriptor", err)
	}
	configured, err := binding.Configure(request)
	if err != nil || len(configured.LocalEnvironment.Skills) != 0 || len(configured.LocalEnvironment.MCP) != 0 || configured.LocalEnvironment.CapabilityRoot != "" {
		t.Fatal("execution accepted caller-supplied Skill paths", err)
	}
	request.LocalEnvironment.Capabilities = true
	request.LocalEnvironment.Skills = nil
	request.WorkspaceReadOnly = true
	if _, err := binding.Configure(request); err != nil {
		t.Fatal("read-only binding required a capability installation", err)
	}
}
