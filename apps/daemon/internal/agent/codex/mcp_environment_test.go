package codex

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

func TestEnvironmentMCPProjectsIsolatedStdioAndPrivateHTTPReferences(t *testing.T) {
	token := "user-token"
	local := &proto.LocalEnvironment{MCP: []proto.EnvironmentMCP{
		{InstallationRoot: "/private/runtime/capabilities", PackageRoot: "plugins/0", Server: proto.PluginMCPServer{Name: "local", Type: "stdio", Command: "must-not-be-native-command", Args: []string{"private-argument"}}},
		{InstallationRoot: "/private/runtime/capabilities", PackageRoot: "plugins/1", BearerToken: &token, Server: proto.PluginMCPServer{Name: "remote", Type: "http", URL: "https://example.com/mcp", HTTPHeaders: map[string]string{"X-Key": "literal-${DO_NOT_EXPAND}"}}},
	}}
	servers, env, err := runtimeMCPServers(proto.PromptRequestPayload{LocalEnvironment: local})
	if err != nil || len(servers) != 2 || len(env) != 1 {
		t.Fatal("environment declarations were not projected", err)
	}
	executable, _ := os.Executable()
	if servers["local"].Command != executable ||
		!slices.Equal(servers["local"].Args, []string{"runtime-mcp-exec", "/private/runtime/capabilities", "plugins/0", "local"}) {
		t.Fatal("native stdio bypasses the packaged launcher")
	}
	remote := servers["remote"]
	if remote.BearerTokenEnvVar == "" || remote.HTTPHeaders["X-Key"] != "literal-${DO_NOT_EXPAND}" ||
		!slices.Contains(env, remote.BearerTokenEnvVar+"="+token) {
		t.Fatal("private header values changed")
	}
	fixture := map[string]any{"config": map[string]any{
		"mcp_oauth_credentials_store": "file",
		"mcp_servers": map[string]any{
			"local":  map[string]any{"command": servers["local"].Command, "args": servers["local"].Args, "environment_id": "local", "enabled": true, "tool_timeout_sec": nil},
			"remote": map[string]any{"url": remote.URL, "environment_id": "local", "enabled": true, "tool_timeout_sec": nil, "bearer_token_env_var": remote.BearerTokenEnvVar, "http_headers": remote.HTTPHeaders},
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

func TestSessionMCPHeadersReachNativeConfig(t *testing.T) {
	req := proto.PromptRequestPayload{DisableExecutionEnvironment: true, MCPHTTPServers: &[]proto.MCPHTTPServer{{ConnectionOrigin: "service", ServerLabel: "remote", ServerURL: "https://example.com/mcp", Headers: map[string]string{"X-Tenant": "a"}}}}
	servers, env, err := runtimeMCPServers(req)
	if err != nil || servers["remote"].HTTPHeaders["X-Tenant"] != "a" || len(env) != 0 {
		t.Fatal("Session headers were not projected", err)
	}
}

func TestEnvironmentMCPRejectsUnqualifiedCredentialChanges(t *testing.T) {
	token := "user-token"
	local := &proto.LocalEnvironment{MCP: []proto.EnvironmentMCP{{BearerToken: &token, Server: proto.PluginMCPServer{Name: "remote", Type: "http", URL: "http://example.com/mcp"}}}}
	if _, _, err := runtimeMCPServers(proto.PromptRequestPayload{LocalEnvironment: local}); err == nil {
		t.Fatal("plaintext bearer accepted")
	}
	local.MCP[0].Server.URL = "https://example.com/mcp"
	if _, _, err := runtimeMCPServers(proto.PromptRequestPayload{LocalEnvironment: local, MCPHTTPServers: &[]proto.MCPHTTPServer{{ConnectionOrigin: "service", ServerLabel: "remote", ServerURL: "https://example.com/mcp"}}}); err == nil {
		t.Fatal("service and environment identity collision accepted")
	}
}
