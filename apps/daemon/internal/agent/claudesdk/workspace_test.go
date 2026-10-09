package claudesdk

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

func workspaceRequest() proto.PromptRequestPayload {
	return proto.PromptRequestPayload{ModelProvider: fixtureProvider(), DisableSubagents: true, LocalEnvironment: &proto.LocalEnvironment{ID: "environment"},
		Model: "fixture"}
}

func TestWorkspaceRetainsDeclaredFunctions(t *testing.T) {
	req := workspaceRequest()
	req.FunctionTools = []proto.FunctionTool{{Name: "lookup", Parameters: json.RawMessage(`{"type":"object"}`)}}
	bound := prepared(t, req)
	bound.WorkspaceRoot = t.TempDir()
	start, _, err := prepareTestView(t, bound)
	if err != nil {
		t.Fatal(err)
	}
	if start.Workspace == nil || len(start.Functions) != 1 || start.Functions[0].Name != "lookup" || start.MCPHTTPServers != nil {
		t.Fatal("workspace function declaration was not retained independently of external MCP")
	}
}

func TestPublicMCPUsesWorkspaceProjectionWithoutCredentialCopy(t *testing.T) {
	req := workspaceRequest()
	token := "vault-selected-canary"
	tools := []string{"prove"}
	req.MCPHTTPServers = &[]proto.MCPHTTPServer{{ConnectionOrigin: "environment", ServerLabel: "remote", ServerURL: "https://example.test/mcp", AllowedTools: &tools, Required: true, BearerToken: &token}}
	bound := prepared(t, req)
	bound.WorkspaceRoot = t.TempDir()
	start, env, err := prepareTestView(t, bound)
	if err != nil {
		t.Fatal(err)
	}
	if start.MCPHTTPServers != nil || start.Workspace == nil || len(start.Workspace.MCP) != 1 || !start.Workspace.MCP[0].Required || (*start.Workspace.MCP[0].AllowedTools)[0] != "prove" {
		t.Fatal("workspace policy lost")
	}
	raw, _ := json.Marshal(start)
	if strings.Contains(string(raw)+strings.Join(env, "\n"), token) {
		t.Fatal("bearer reached the bridge")
	}
	info := RuntimeInfo{Protocol: 4, Features: []string{"workspace_tools", "workspace_prepare", "workspace_command_observations", "local_runtime_v2", "mcp_http_tools", "mcp_http_bearer_auth", "mcp_http_required"}}
	if validateExecutorFeatures(info, start) == nil {
		t.Fatal("unqualified workspace bridge admitted")
	}
	info.Features = append(info.Features, "workspace_mcp_http")
	if err := validateExecutorFeatures(info, start); err != nil {
		t.Fatal(err)
	}
}
