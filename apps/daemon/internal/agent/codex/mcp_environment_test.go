package codex

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentplugin"
)

// TestViewMCPProjectsStdioAliasAndGatewayURL checks that the view's stdio
// server runs as its alias without arguments and its HTTP server at the
// gateway URL, and that the qualified native configuration matches only that
// projection.
func TestViewMCPProjectsStdioAliasAndGatewayURL(t *testing.T) {
	cfg := testView(t, "", t.TempDir())
	cfg.view.MCP = []agent.MCPBinding{
		{ServerLabel: "local", ConnectionOrigin: "environment", CredentialAuthority: "none", Transport: "stdio", Stdio: &agent.EnvironmentMCP{
			Server: agentplugin.MCPServer{Name: "local", Type: "stdio", Command: agent.ViewAlias(0)}}},
		{ServerLabel: "remote", ConnectionOrigin: "environment", CredentialAuthority: "environment_configuration", Transport: "http", ServerURL: "http://127.0.0.1:17102/mcp"},
	}
	req := prepared(t, "state", proto.PromptRequestPayload{Model: "fixture", ModelProvider: fixtureProvider(), LocalEnvironment: &proto.LocalEnvironment{}})
	req.WorkspaceRoot = "/workspace"
	plan, err := prepareViewPlan(t.Context(), req, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer plan.Cleanup()
	servers := plan.mcpServers
	local, remote := servers["local"], servers["remote"]
	if len(servers) != 2 || local.Command != agent.ViewAlias(0) || local.Args == nil || len(local.Args) != 0 || !local.ApproveTools ||
		remote.URL != "http://127.0.0.1:17102/mcp" {
		t.Fatalf("view MCP projection: %+v", servers)
	}
	fixture := map[string]any{"config": map[string]any{
		"mcp_oauth_credentials_store": "file", "features": map[string]bool{"plugins": false, "apps": false},
		"mcp_servers": map[string]any{
			"local":  map[string]any{"command": local.Command, "args": []string{}, "environment_id": "local", "enabled": true, "tool_timeout_sec": nil, "default_tools_approval_mode": "approve"},
			"remote": map[string]any{"url": remote.URL, "environment_id": "local", "enabled": true, "tool_timeout_sec": nil, "default_tools_approval_mode": "approve"},
		},
	}}
	raw, _ := json.Marshal(fixture)
	if !matchesMCPConfig(raw, servers) {
		t.Fatal("qualified native projection rejected")
	}
	corrupt := strings.Replace(string(raw), agent.ViewAlias(0), "/usr/bin/untrusted-launcher", 1)
	if matchesMCPConfig(json.RawMessage(corrupt), servers) {
		t.Fatal("different native launcher accepted")
	}
}
