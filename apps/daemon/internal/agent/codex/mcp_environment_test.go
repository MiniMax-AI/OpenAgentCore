package codex

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentplugin"
)

func TestEnvironmentMCPProjectsIsolatedStdioAndPrivateHTTPReferences(t *testing.T) {
	token := "user-token"
	req := agent.PrepareRequest{PromptRequestPayload: proto.PromptRequestPayload{LocalEnvironment: &proto.LocalEnvironment{}}, MCP: []agent.EnvironmentMCP{
		{InstallationRoot: "/private/runtime/capabilities", WorkspaceRoot: "/private/runtime/workspace", PackageRoot: "plugins/0", Server: agentplugin.MCPServer{Name: "local", Type: "stdio", Command: "must-not-be-native-command", Args: []string{"private-argument"}}},
		{InstallationRoot: "/private/runtime/capabilities", WorkspaceRoot: "/private/runtime/workspace", PackageRoot: "plugins/1", BearerToken: &token, Server: agentplugin.MCPServer{Name: "remote", Type: "http", URL: "https://example.com/mcp", HTTPHeaders: map[string]string{"X-Key": "literal-${DO_NOT_EXPAND}"}}},
	}}
	servers, env, err := runtimeMCPServers(req)
	if err != nil || len(servers) != 2 || len(env) != 2 {
		t.Fatal("environment declarations were not projected", err)
	}
	executable, _ := os.Executable()
	if servers["local"].Command != executable || !servers["local"].ApproveTools ||
		!slices.Equal(servers["local"].Args, []string{"runtime-mcp-exec", "/private/runtime/capabilities", "plugins/0", "local"}) {
		t.Fatal("native stdio bypasses the packaged launcher")
	}
	remote := servers["remote"]
	if remote.BearerTokenEnvVar == "" || remote.EnvHTTPHeaders["X-Key"] == "" ||
		!slices.Contains(env, remote.BearerTokenEnvVar+"="+token) ||
		!slices.Contains(env, remote.EnvHTTPHeaders["X-Key"]+"=literal-${DO_NOT_EXPAND}") {
		t.Fatal("private header values changed")
	}
	fixture := map[string]any{"config": map[string]any{
		"mcp_oauth_credentials_store": "file", "features": map[string]bool{"plugins": false, "apps": false},
		"mcp_servers": map[string]any{
			"local":  map[string]any{"command": servers["local"].Command, "args": servers["local"].Args, "environment_id": "local", "enabled": true, "tool_timeout_sec": nil, "default_tools_approval_mode": "approve"},
			"remote": map[string]any{"url": remote.URL, "environment_id": "local", "enabled": true, "tool_timeout_sec": nil, "default_tools_approval_mode": "approve", "bearer_token_env_var": remote.BearerTokenEnvVar, "env_http_headers": remote.EnvHTTPHeaders},
		},
	}}
	raw, _ := json.Marshal(fixture)
	if !matchesMCPConfig(raw, servers) {
		t.Fatal("qualified native projection rejected")
	}
	corrupt := strings.Replace(string(raw), "runtime-mcp-exec", "untrusted-launcher", 1)
	if matchesMCPConfig(json.RawMessage(corrupt), servers) {
		t.Fatal("different native launcher accepted")
	}
}

func TestEnvironmentMCPRejectsPlaintextBearerAndCollisions(t *testing.T) {
	token := "user-token"
	req := agent.PrepareRequest{PromptRequestPayload: proto.PromptRequestPayload{LocalEnvironment: &proto.LocalEnvironment{}},
		MCP: []agent.EnvironmentMCP{{BearerToken: &token, Server: agentplugin.MCPServer{Name: "remote", Type: "http", URL: "http://example.com/mcp"}}}}
	if _, _, err := runtimeMCPServers(req); err == nil {
		t.Fatal("plaintext bearer accepted")
	}
	req.MCP[0].Server.URL = "https://example.com/mcp"
	req.MCPHTTPServers = &[]proto.MCPHTTPServer{{ConnectionOrigin: "service", ServerLabel: "remote", ServerURL: "https://example.com/mcp"}}
	if _, _, err := runtimeMCPServers(req); err == nil {
		t.Fatal("service and environment identity collision accepted")
	}
}
