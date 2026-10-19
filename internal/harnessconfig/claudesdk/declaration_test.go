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
		"tool_search with multi_agent":       {MultiAgent: true, Tools: tools("tool_search")},
	} {
		if admission.Environment == nil {
			admission.Environment = &harnessconfig.AdmissionEnvironment{Type: "none"}
		}
		admission.Model = "model"
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
