package claudesdk

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentplugin"
)

func TestEnvironmentMCPUsesInstalledLauncherAndSelectedCredential(t *testing.T) {
	config := workspaceFixture(t)
	config.Workspace.NetworkAccess = "enabled"
	req := workspaceRequest()
	token := "selected-user-token"
	t.Setenv("MCP_TOKEN", "unselected-native-token")
	req.LocalEnvironment = &proto.LocalEnvironment{NetworkAccess: "enabled", MCP: []proto.EnvironmentMCP{
		{PackageRoot: "plugins/local", Server: agentplugin.MCPServer{Name: "local", Type: "stdio", Command: "untrusted-package-command", Args: []string{"package-argument"}, EnvVars: []string{"MCP_TOKEN"}}},
		{PackageRoot: "plugins/remote", Server: agentplugin.MCPServer{Name: "remote", Type: "http", URL: "https://example.invalid/mcp", BearerTokenEnvVar: "MCP_TOKEN"}, BearerToken: &token},
	}}
	start, env, err := prepare(config, req)
	if err != nil {
		t.Fatal(err)
	}
	if start.MCPHTTPServers != nil || len(start.Workspace.MCP) != 2 {
		t.Fatal("environment declarations changed authority")
	}
	stdio := start.Workspace.MCP[0]
	if stdio.Command != "/usr/bin/python3" || len(stdio.Args) != 6 || stdio.Args[4] != "plugins/local" || stdio.Args[5] != "local" {
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
		if strings.Contains(entry, "unselected-native-token") {
			t.Fatal("inherited native credential")
		}
	}
	if !found || !strings.HasPrefix(reference, "PARSAR_MCP_BEARER_") || len(start.declaredMCP()) != 2 {
		t.Fatal("selected credential or observation declarations missing")
	}
}

func TestEnvironmentMCPRejectsUnqualifiedCombinations(t *testing.T) {
	for _, mutate := range []func(*proto.LocalEnvironment){
		func(e *proto.LocalEnvironment) { e.NetworkAccess = "restricted" },
		func(e *proto.LocalEnvironment) { e.MCP[0].Server.Name = "functions" },
		func(e *proto.LocalEnvironment) { e.MCP = append(e.MCP, e.MCP[0]) },
		func(e *proto.LocalEnvironment) { e.MCP[0].Server.Type = "sse" },
		func(e *proto.LocalEnvironment) { e.MCP[0].Server.HTTPHeaders = map[string]string{"X-Key": "literal"} },
		func(e *proto.LocalEnvironment) { e.MCP[0].Server.BearerTokenEnvVar = "MISSING" },
		func(e *proto.LocalEnvironment) {
			token := "token"
			e.MCP[0].BearerToken = &token
			e.MCP[0].Server.URL = "http://example.invalid/mcp"
		},
	} {
		environment := &proto.LocalEnvironment{NetworkAccess: "enabled", MCP: []proto.EnvironmentMCP{{PackageRoot: "plugins/remote", Server: agentplugin.MCPServer{Name: "remote", Type: "http", URL: "https://example.invalid/mcp"}}}}
		mutate(environment)
		if _, _, err := prepareEnvironmentMCP(environment); err == nil {
			t.Fatal("unsupported declaration accepted")
		}
	}
}

func TestEnvironmentMCPObservationsUseInstalledDeclarations(t *testing.T) {
	start := startRequest{Workspace: &workspaceProfile{MCP: []environmentMCPServer{{mcpHTTPServer: mcpHTTPServer{ServerLabel: "installed"}}}}, observeFunctions: true}
	state := mcpState{calls: map[string]proto.ToolObservation{}}
	observation := proto.ToolObservation{Kind: "mcp", Name: "echo", Server: "installed", Status: "in_progress", Arguments: json.RawMessage(`{}`), Output: json.RawMessage(`null`), Error: json.RawMessage(`null`)}
	var emitted []proto.ToolCallPayload
	emit := func(_ string, payload any) { emitted = append(emitted, payload.(proto.ToolCallPayload)) }
	if err := state.receive(bridgeEvent{ID: "native-call", Stage: "before", Observation: &observation}, start, emit); err != nil {
		t.Fatal(err)
	}
	state.close(start, emit)
	if len(emitted) != 2 || emitted[1].ID != "native-call" || emitted[1].Observation.Status != "incomplete" {
		t.Fatal("interrupted environment call lost identity")
	}
	observation.Server = "undeclared"
	if err := state.receive(bridgeEvent{ID: "other", Stage: "before", Observation: &observation}, start, emit); err == nil {
		t.Fatal("undeclared observation accepted")
	}
}
