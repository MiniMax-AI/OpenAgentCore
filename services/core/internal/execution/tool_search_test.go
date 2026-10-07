package execution

import (
	"encoding/json"
	"strings"
	"testing"
)

const discoveryConfiguration = `{"agent":{"model":"model","tools":[{"type":"tool_search"},{"type":"function","name":"lookup","description":"Lookup","parameters":{"type":"object"},"defer_loading":true},{"type":"function","name":"clock","description":"Clock","parameters":{"type":"object"}}]},"environment":{"type":"none"}}`

func TestDiscoveryKeepsMixedFunctionDefinitions(t *testing.T) {
	var snapshot Snapshot
	if err := json.Unmarshal([]byte(discoveryConfiguration), &snapshot); err != nil {
		t.Fatal(err)
	}
	tools, err := executionTools(snapshot.Agent.Tools)
	if err != nil || len(tools.MCP) != 0 || !tools.Search || len(tools.Functions) != 2 || !tools.Functions[0].DeferLoading || tools.Functions[1].DeferLoading {
		t.Fatal(tools, err)
	}
	for _, raw := range []string{
		strings.Replace(discoveryConfiguration, `{"type":"tool_search"},`, "", 1),
		strings.Replace(discoveryConfiguration, `"defer_loading":true`, `"defer_loading":false`, 1),
		strings.Replace(discoveryConfiguration, `{"type":"tool_search"}`, `{"type":"tool_search","execution":"client"}`, 1),
		strings.Replace(discoveryConfiguration, `{"type":"tool_search"}`, `{"type":"tool_search"},{"type":"tool_search"}`, 1),
	} {
		if err := ValidateSessionConfiguration("claude_sdk", json.RawMessage(raw)); err == nil {
			t.Fatal("unqualified discovery accepted", raw)
		}
	}
}
