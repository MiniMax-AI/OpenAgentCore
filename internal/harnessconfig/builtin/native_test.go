package builtin

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig"
)

func TestNativeModelParameters(t *testing.T) {
	for _, tc := range []struct {
		kind, raw string
		valid     bool
	}{
		{"codex", `{"model_reasoning_effort":"high"}`, true},
		{"codex", `{"model_reasoning_effort":"private-sentinel","model_reasoning_effort":"high"}`, false},
		{"claude_sdk", `{"thinking":{"type":"private-sentinel","type":"enabled"}}`, false},
		{"codex", `{"model_reasoning_effort":{}}`, false},
		{"codex", `{"model_reasoning_effort":"max"}`, false},
		{"codex", `{"effort":"high"}`, false},
		{"claude_sdk", `{"effort":"max","thinking":{"type":"adaptive","display":"summarized"}}`, true},
		{"claude_sdk", `{"thinking":{"type":"enabled","budgetTokens":1024}}`, true},
		{"claude_sdk", `{"thinking":{"type":"disabled"}}`, true},
		{"claude_sdk", `{"thinking":{"type":"enabled"}}`, true},
		{"claude_sdk", `{"thinking":{"type":"adaptive","budgetTokens":1024}}`, false},
		{"claude_sdk", `{"thinking":{"type":"enabled","budgetTokens":1.5}}`, false},
		{"claude_sdk", `{"thinking":{"type":"disabled","display":"omitted"}}`, false},
		{"claude_sdk", `{"thinking":{"type":"adaptive","display":{}}}`, false},
		{"claude_sdk", `{"thinking":{"type":"adaptive","env":{}}}`, false},
		{"claude_sdk", `{"effort":{}}`, false},
		{"claude_sdk", `{"maxThinkingTokens":1024}`, false},
		{"mcode", `{}`, true},
		{"mcode", `{"effort":"high"}`, false},
		{"", `{"effort":"high"}`, true},
		{"", `{"model_reasoning_effort":"high"}`, true},
		{"", `{"secret":"private-value"}`, false},
		{"unknown", `{}`, true},
		{"unknown", `{"effort":"high"}`, false},
	} {
		err := Registry().ValidateHarnessConfig(tc.kind, json.RawMessage(tc.raw))
		if (err == nil) != tc.valid {
			t.Fatalf("%s %s: %v", tc.kind, tc.raw, err)
		}
		if err != nil && err != harnessconfig.ErrHarnessConfig {
			t.Fatalf("unsafe error: %v", err)
		}
	}
}

func TestNativeConfigurationBoundary(t *testing.T) {
	for _, kind := range []string{"codex", "claude_sdk", "mcode", ""} {
		for _, raw := range []string{"null", "[]", "1", `"value"`, `{"unknown":"` + strings.Repeat("x", harnessconfig.MaxHarnessConfigBytes) + `"}`} {
			if err := Registry().ValidateHarnessConfig(kind, json.RawMessage(raw)); err != harnessconfig.ErrHarnessConfig {
				t.Fatalf("%s accepted invalid shape: %v", kind, err)
			}
		}
		for _, key := range []string{"model", "model_provider", "api_key", "env", "hooks", "mcp_servers", "cwd", "permissions", "maxTurns", "model_verbosity", "web_search"} {
			raw, _ := json.Marshal(map[string]any{key: "private-sentinel"})
			if err := Registry().ValidateHarnessConfig(kind, raw); err != harnessconfig.ErrHarnessConfig {
				t.Fatalf("%s accepted reserved parameter %s", kind, key)
			}
		}
		if Registry().ValidateHarnessConfig(kind, nil) != nil || Registry().ValidateHarnessConfig(kind, json.RawMessage(`{}`)) != nil {
			t.Fatal("empty config must be supported")
		}
	}
}

func TestPreparedConfigurationOwnsNestedSnapshot(t *testing.T) {
	c, _ := Registry().Lookup("claude_sdk")
	original := map[string]any{"thinking": map[string]any{"type": "enabled", "budgetTokens": 1024}}
	prepared, err := c.PrepareHarnessConfig(map[string]any{"harness_config": original})
	if err != nil {
		t.Fatal(err)
	}
	original["thinking"].(map[string]any)["budgetTokens"] = 2048
	if prepared["thinking"].(map[string]any)["budgetTokens"] != float64(1024) {
		t.Fatal("configuration retained caller ownership")
	}
	if _, err = c.PrepareHarnessConfig(map[string]any{"harness_config": nil}); err == nil {
		t.Fatal("null accepted")
	}
}
