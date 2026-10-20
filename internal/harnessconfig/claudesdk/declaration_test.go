package claudesdk

import (
	"encoding/json"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig"
)

func TestDeclarationAdmitsNativeToolCombinations(t *testing.T) {
	tools := func(types ...string) []json.RawMessage {
		var raw []json.RawMessage
		for _, kind := range types {
			raw = append(raw, json.RawMessage(`{"type":"`+kind+`"}`))
		}
		return raw
	}
	for name, admission := range map[string]harnessconfig.Admission{
		"structured output with multi_agent": {MultiAgent: true, TextFormat: "json_schema"},
		"structured output with mcp":         {TextFormat: "json_schema", Tools: tools("mcp")},
		"structured output with tool_search": {TextFormat: "json_schema", Tools: tools("function", "tool_search")},
		"tool_search with mcp":               {Tools: tools("function", "tool_search", "mcp")},
	} {
		admission.TextSchema = json.RawMessage(`{"type":"object"}`)
		if err := Declaration.ValidateConfiguration(admission); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestDeclarationAdmitsAnyMCPToolName(t *testing.T) {
	allowed := []string{"a/b", "tool name"}
	err := Declaration.ValidateTools(nil, []proto.MCPHTTPServer{{ServerLabel: "docs", ServerURL: "https://example.test/mcp", AllowedTools: &allowed}})
	if err != nil {
		t.Fatal(err)
	}
}

func TestDeclarationRejectsMCPEnvironmentExpansion(t *testing.T) {
	servers := []proto.MCPHTTPServer{{ServerLabel: "docs", ServerURL: "https://example.test/mcp", Headers: map[string]string{"X-Tenant": "a"}}}
	if err := Declaration.ValidateTools(nil, servers); err != nil {
		t.Fatal(err)
	}
	servers[0].Headers["X-Tenant"] = "${ANTHROPIC_API_KEY}"
	if err := Declaration.ValidateTools(nil, servers); err == nil {
		t.Fatal("header value with ${ admitted")
	}
	servers[0].Headers["X-Tenant"], servers[0].ServerURL = "a", "https://example.test/${ANTHROPIC_API_KEY}"
	if err := Declaration.ValidateTools(nil, servers); err == nil {
		t.Fatal("URL with ${ admitted")
	}
}

func TestDeclarationRequiresWorkspaceStdioCwd(t *testing.T) {
	stdio := func(cwd string) []json.RawMessage {
		return []json.RawMessage{json.RawMessage(`{"type":"mcp","transport":{"type":"stdio","command":"x","cwd":"` + cwd + `"}}`)}
	}
	if err := Declaration.ValidateConfiguration(harnessconfig.Admission{WorkspaceDirectory: "/workspace", Tools: stdio("/workspace/")}); err != nil {
		t.Fatal(err)
	}
	if err := Declaration.ValidateConfiguration(harnessconfig.Admission{WorkspaceDirectory: "/workspace", Tools: stdio("/srv")}); err == nil {
		t.Fatal("stdio cwd outside the workspace admitted")
	}
}
