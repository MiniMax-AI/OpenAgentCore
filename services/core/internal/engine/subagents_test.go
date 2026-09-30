package engine

import (
	"encoding/json"
	"testing"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
)

func TestSubagentProfilesPreserveQualifiedOperationBoundaries(t *testing.T) {
	for _, kind := range []string{"codex", "claude_sdk", "mcode"} {
		t.Run(kind, func(t *testing.T) {
			profile, ok := (Catalog{}).Lookup(kind)
			if !ok {
				t.Fatal("profile missing")
			}
			agent := v1.Agent{Model: "real-model"}
			agent.MultiAgent.Enabled = true
			for _, placement := range []string{"none", "openai_hosted", "self_hosted"} {
				if err := profile.ValidateConfiguration(agent, &v1.Environment{Type: placement}, false); err != nil {
					t.Fatal(placement, err)
				}
			}
			// Public functions and MCP remain independently qualified capabilities; new
			// child observation support does not automatically admit their combinations.
			if kind == "mcode" {
				return
			} // Its existing tool profile rejects both for every Session.
			for _, tool := range []string{`{"type":"function","name":"f"}`, `{"type":"mcp","server_label":"s"}`} {
				agent.Tools = []json.RawMessage{json.RawMessage(tool)}
				if err := profile.ValidateConfiguration(agent, &v1.Environment{Type: "none"}, false); err == nil {
					t.Fatal("unqualified multi-agent combination accepted", tool)
				}
				agent.MultiAgent.Enabled = false
				if err := profile.ValidateConfiguration(agent, &v1.Environment{Type: "none"}, false); err != nil {
					t.Fatal("single-agent profile changed", err)
				}
				agent.MultiAgent.Enabled = true
			}
		})
	}
}
