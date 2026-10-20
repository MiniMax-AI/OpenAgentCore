package builtin

import (
	"encoding/json"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig"
)

func TestMCPOriginSupport(t *testing.T) {
	for _, kind := range []string{"claude_sdk", "codex", "mcode"} {
		declaration, _ := Registry().Lookup(kind)
		for _, placement := range []string{"none", "self_hosted", "openai_hosted"} {
			for _, origin := range []string{"service", "environment", "", "unknown"} {
				servers := []proto.MCPHTTPServer{{ConnectionOrigin: origin, ServerLabel: "proof", ServerURL: "https://example.test/mcp"}}
				allowed := origin == "environment" && placement != "none" || origin == "service" && placement == "none"
				if err := declaration.ValidateMCPOrigins(placement, servers); (err == nil) != allowed {
					t.Fatalf("%s/%s/%s: %v", kind, placement, origin, err)
				}
			}
		}
	}
}

func TestMiniMaxMCPPoliciesRejectInsteadOfDropping(t *testing.T) {
	declaration, _ := Registry().Lookup("mcode")
	empty := []string{}
	named := []string{"proof"}
	for _, allowed := range []*[]string{nil, &empty, &named} {
		for _, required := range []bool{false, true} {
			server := proto.MCPHTTPServer{ConnectionOrigin: "environment", ServerLabel: "proof", ServerURL: "https://example.test", AllowedTools: allowed, Required: required}
			err := declaration.ValidateTools(nil, []proto.MCPHTTPServer{server})
			if (err == nil) != (allowed == nil && !required) {
				t.Fatal("unsupported MCP policy accepted", err)
			}
		}
	}
}

func TestSubagentDeclarationsPreserveSupportedBoundaries(t *testing.T) {
	for _, kind := range []string{"claude_sdk", "codex"} {
		t.Run(kind, func(t *testing.T) {
			declaration, _ := Registry().Lookup(kind)
			admission := harnessconfig.Admission{MultiAgent: true}
			if err := declaration.ValidateConfiguration(admission); err != nil {
				t.Fatal(err)
			}
			// Subagents use the Agent's function and MCP tools.
			for _, tool := range []string{`{"type":"function","name":"f"}`, `{"type":"mcp","server_label":"s"}`} {
				admission.Tools = []json.RawMessage{json.RawMessage(tool)}
				if err := declaration.ValidateConfiguration(admission); err != nil {
					t.Fatal(kind, tool, err)
				}
			}
		})
	}
}

func TestCodexRejectsCodeModeWithSubagents(t *testing.T) {
	declaration, _ := Registry().Lookup("codex")
	tools := []json.RawMessage{json.RawMessage(`{"type":"programmatic_tool_calling","enabled":true}`)}
	for _, multiAgent := range []bool{false, true} {
		err := declaration.ValidateConfiguration(harnessconfig.Admission{MultiAgent: multiAgent, Tools: tools})
		if (err == nil) == multiAgent {
			t.Fatalf("multi_agent=%v: %v", multiAgent, err)
		}
	}
}
