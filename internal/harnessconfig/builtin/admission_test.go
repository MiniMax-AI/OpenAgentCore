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
				allowed := origin == "environment" && placement != "none" || origin == "service" && placement == "none" && kind != "mcode"
				if err := declaration.ValidateMCPOrigins(&harnessconfig.AdmissionEnvironment{Type: placement}, servers); (err == nil) != allowed {
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
	for _, kind := range []string{"claude_sdk", "codex", "mcode"} {
		t.Run(kind, func(t *testing.T) {
			declaration, _ := Registry().Lookup(kind)
			admission := harnessconfig.Admission{Model: "real-model", MultiAgent: true}
			for _, placement := range []string{"none", "openai_hosted", "self_hosted"} {
				admission.Environment = &harnessconfig.AdmissionEnvironment{Type: placement}
				if err := declaration.ValidateConfiguration(admission); err != nil {
					t.Fatal(placement, err)
				}
			}
			// Public functions and MCP stay independent capabilities: Subagent
			// support does not admit their combinations.
			if kind == "mcode" {
				return
			} // Its tool declaration rejects both for every Session.
			admission.Environment = &harnessconfig.AdmissionEnvironment{Type: "none"}
			for _, tool := range []string{`{"type":"function","name":"f"}`, `{"type":"mcp","server_label":"s"}`} {
				admission.Tools = []json.RawMessage{json.RawMessage(tool)}
				admission.MultiAgent = true
				if err := declaration.ValidateConfiguration(admission); err == nil {
					t.Fatal("unsupported multi-agent combination accepted", tool)
				}
				admission.MultiAgent = false
				if err := declaration.ValidateConfiguration(admission); err != nil {
					t.Fatal("single-agent declaration changed", err)
				}
			}
		})
	}
}
