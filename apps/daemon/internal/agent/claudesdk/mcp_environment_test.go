package claudesdk

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentplugin"
)

func TestEnvironmentMCPUsesInstalledLauncherAndSelectedCredential(t *testing.T) {
	config := workspaceFixture(t)
	config.Workspace.NetworkAccess = "enabled"
	req := workspaceRequest()
	token := "selected-user-token"
	t.Setenv("MCP_TOKEN", "unselected-native-token")
	req.LocalEnvironment = &proto.LocalEnvironment{}
	bound := prepared(t, req)
	bound.CapabilityRoot, bound.WorkspaceRoot = "/private/runtime/capabilities", config.Workspace.Directory
	bound.MCP = []agent.EnvironmentMCP{
		{InstallationRoot: "/private/runtime/capabilities", WorkspaceRoot: "/private/runtime/workspace", PackageRoot: "plugins/local", Server: agentplugin.MCPServer{Name: "local", Type: "stdio", Command: "untrusted-package-command", Args: []string{"package-argument"}, EnvVars: []string{"MCP_TOKEN"}}},
		{InstallationRoot: "/private/runtime/capabilities", WorkspaceRoot: "/private/runtime/workspace", PackageRoot: "plugins/remote", Server: agentplugin.MCPServer{Name: "remote", Type: "http", URL: "https://example.invalid/mcp", BearerTokenEnvVar: "MCP_TOKEN"}, BearerToken: &token},
	}
	start, env, err := prepareConfiguration(config, bound)
	if err != nil {
		t.Fatal(err)
	}
	if start.MCPHTTPServers != nil || len(start.Workspace.MCP) != 2 || start.Workspace.CapabilityRoot != bound.CapabilityRoot {
		t.Fatal("environment declarations changed authority")
	}
	stdio := start.Workspace.MCP[0]
	executable, _ := os.Executable()
	if stdio.Command != executable || len(stdio.Args) != 4 || stdio.Args[0] != "runtime-mcp-exec" || stdio.Args[1] != "/private/runtime/capabilities" || stdio.Args[2] != "plugins/local" || stdio.Args[3] != "local" {
		t.Fatal("stdio bypassed the shared installed entry")
	}
	raw, _ := json.Marshal(start)
	for _, forbidden := range []string{token, "unselected-native-token", "untrusted-package-command", "package-argument", `"MCP_TOKEN"`} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatal("private request contains package input or credential values")
		}
	}
	reference := start.Workspace.MCP[1].BearerTokenEnvVar
	found := false
	for _, entry := range env {
		if entry == reference+"="+token {
			found = true
		}
	}
	if !found || !strings.HasPrefix(reference, "OAC_RUNTIME_MCP_BEARER_") || len(start.declaredMCP()) != 2 {
		t.Fatal("selected credential or observation declarations missing")
	}
}

func TestEnvironmentMCPRejectsUnqualifiedCombinations(t *testing.T) {
	for _, mutate := range []func(*agent.PrepareRequest){
		func(r *agent.PrepareRequest) { r.MCP = append(r.MCP, r.MCP[0]) },
		func(r *agent.PrepareRequest) { r.MCP[0].Server.Type = "sse" },
		func(r *agent.PrepareRequest) { r.MCP[0].Server.HTTPHeaders = map[string]string{"X-Key": "literal"} },
		func(r *agent.PrepareRequest) { r.MCP[0].Server.BearerTokenEnvVar = "MISSING" },
		func(r *agent.PrepareRequest) {
			token := "token"
			r.MCP[0].BearerToken = &token
			r.MCP[0].Server.URL = "http://example.invalid/mcp"
		},
	} {
		req := agent.PrepareRequest{PromptRequestPayload: proto.PromptRequestPayload{LocalEnvironment: &proto.LocalEnvironment{}}, MCP: []agent.EnvironmentMCP{{InstallationRoot: "/private/runtime/capabilities", WorkspaceRoot: "/private/runtime/workspace", PackageRoot: "plugins/remote", Server: agentplugin.MCPServer{Name: "remote", Type: "http", URL: "https://example.invalid/mcp"}}}}
		mutate(&req)
		if _, _, err := prepareRuntimeMCP(req); err == nil {
			t.Fatal("unsupported declaration accepted")
		}
	}
}

func TestEnvironmentMCPObservationsUseInstalledDeclarations(t *testing.T) {
	start := startRequest{Workspace: &workspaceProfile{MCP: []environmentMCPServer{{mcpHTTPServer: mcpHTTPServer{ServerLabel: "installed"}}}}}
	state := mcpState{calls: map[string]proto.ToolObservation{}}
	observation := proto.ToolObservation{Kind: "mcp", Name: "echo", Server: "installed", Status: "in_progress", Arguments: json.RawMessage(`{}`), Output: json.RawMessage(`null`), Error: json.RawMessage(`null`)}
	var emitted []proto.ToolCallPayload
	emit := func(_ string, payload any) { emitted = append(emitted, payload.(proto.ToolCallPayload)) }
	if err := state.receive(bridgeEvent{ID: "native-call", Stage: "before", Observation: &observation}, start, emit); err != nil {
		t.Fatal(err)
	}
	state.close(emit)
	if len(emitted) != 2 || emitted[1].ID != "native-call" || emitted[1].Observation.Status != "incomplete" {
		t.Fatal("interrupted environment call lost identity")
	}
	observation.Server = "undeclared"
	if err := state.receive(bridgeEvent{ID: "other", Stage: "before", Observation: &observation}, start, emit); err == nil {
		t.Fatal("undeclared observation accepted")
	}
}
