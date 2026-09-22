package execution

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/engine"
)

const discoveryConfiguration = `{"agent":{"model":"model","tools":[{"type":"tool_search"},{"type":"function","name":"lookup","description":"Lookup","parameters":{"type":"object"},"defer_loading":true},{"type":"function","name":"clock","description":"Clock","parameters":{"type":"object"}}]},"environment":{"type":"none"}}`

func TestDiscoveryUsesSharedOperationQualification(t *testing.T) {
	for _, qualified := range []bool{false, true} {
		policy := Policy{Engines: engine.NewCatalog(map[string]engine.Profile{"new_harness": {Placements: []string{"none"}, ToolSearch: qualified}})}
		if err := policy.ValidateSessionConfiguration("new_harness", json.RawMessage(discoveryConfiguration)); (err == nil) != qualified {
			t.Fatal("common operation qualification was not applied", qualified, err)
		}
	}
	workspace := strings.Replace(discoveryConfiguration, `"type":"none"`, `"type":"openai_hosted"`, 1)
	policy := Policy{Engines: engine.NewCatalog(map[string]engine.Profile{"new_harness": {Placements: []string{"openai_hosted"}, ToolSearch: true}})}
	if err := policy.ValidateSessionConfiguration("new_harness", json.RawMessage(workspace)); err != nil {
		t.Fatal("Core imposed another adapter's placement restriction", err)
	}
	for _, kind := range []string{"codex", "claude_sdk", "mcode"} {
		if err := (Policy{}).ValidateSessionConfiguration(kind, json.RawMessage(discoveryConfiguration)); (err == nil) != (kind == "claude_sdk") {
			t.Fatal(kind, err)
		}
	}
}

func TestDiscoveryKeepsMixedFunctionDefinitions(t *testing.T) {
	var snapshot Snapshot
	if err := json.Unmarshal([]byte(discoveryConfiguration), &snapshot); err != nil {
		t.Fatal(err)
	}
	functions, mcp, search, err := executionTools(snapshot.Agent.Tools)
	if err != nil || len(mcp) != 0 || !search || len(functions) != 2 || !functions[0].DeferLoading || functions[1].DeferLoading {
		t.Fatal(functions, mcp, search, err)
	}
	request := proto.PromptRequestPayload{ToolSearch: search, FunctionTools: functions}
	if request.ValidateToolSearch(true) != nil || request.ValidateToolSearch(false) == nil {
		t.Fatal("Runtime support is not operation-specific")
	}
	for _, raw := range []string{
		strings.Replace(discoveryConfiguration, `{"type":"tool_search"},`, "", 1),
		strings.Replace(discoveryConfiguration, `"defer_loading":true`, `"defer_loading":false`, 1),
		strings.Replace(discoveryConfiguration, `{"type":"tool_search"}`, `{"type":"tool_search","execution":"client"}`, 1),
		strings.Replace(discoveryConfiguration, `{"type":"tool_search"}`, `{"type":"tool_search"},{"type":"tool_search"}`, 1),
	} {
		if err := (Policy{}).ValidateSessionConfiguration("claude_sdk", json.RawMessage(raw)); err == nil {
			t.Fatal("unqualified discovery accepted", raw)
		}
	}
}
