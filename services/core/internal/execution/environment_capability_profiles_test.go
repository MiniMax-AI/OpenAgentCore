package execution

import (
	"encoding/json"
	"testing"
)

func TestQualifiedClaudeCapabilitiesDoNotDependOnEnvironmentSource(t *testing.T) {
	for _, placement := range []string{"none", "openai_hosted", "self_hosted"} {
		t.Run(placement, func(t *testing.T) {
			for _, feature := range []string{"structured", "discovery"} {
				agent := map[string]any{"model": "fixture", "multi_agent": map[string]bool{"enabled": false}}
				if feature == "structured" {
					agent["text"] = map[string]any{"format": map[string]any{"type": "json_schema", "name": "answer", "strict": true, "schema": map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false}}}
				} else {
					agent["tools"] = []any{map[string]any{"type": "tool_search"}, map[string]any{"type": "function", "name": "lookup", "parameters": map[string]any{"type": "object"}, "defer_loading": true}}
				}
				environment := map[string]any{"type": placement}
				if placement == "self_hosted" {
					environment["workspace_directory"] = "/workspace"
				}
				raw, _ := json.Marshal(map[string]any{"agent": agent, "environment": environment})
				if err := (Policy{}).ValidateSessionConfiguration("claude_sdk", raw); err != nil {
					t.Fatalf("%s: %v", feature, err)
				}
				agent["multi_agent"] = map[string]bool{"enabled": true}
				raw, _ = json.Marshal(map[string]any{"agent": agent, "environment": environment})
				if err := (Policy{}).ValidateSessionConfiguration("claude_sdk", raw); err == nil {
					t.Fatalf("%s with Subagents accepted", feature)
				}
			}
		})
	}
}
