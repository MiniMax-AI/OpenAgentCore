package claudesdk

import (
	"encoding/json"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig"
	"path/filepath"
	"testing"
)

func TestHarnessConfigReachesBridgeAndOwnsSnapshot(t *testing.T) {
	root := t.TempDir()
	t.Setenv("OAC_RUNTIME_HOME", root)
	config := Config{Entrypoint: filepath.Join(root, "main.js"), StateDir: filepath.Join(root, "state")}
	native := map[string]any{"effort": "high", "thinking": map[string]any{"type": "enabled", "budgetTokens": 1024}}
	req := proto.PromptRequestPayload{AgentOptions: map[string]any{"model": "fixture", "harness_config": native}}
	start, _, err := prepareConfiguration(config, req)
	if err != nil {
		t.Fatal(err)
	}
	native["thinking"].(map[string]any)["budgetTokens"] = 2048
	raw, err := json.Marshal(start)
	if err != nil {
		t.Fatal(err)
	}
	var bridge map[string]any
	if json.Unmarshal(raw, &bridge) != nil {
		t.Fatal("invalid bridge request")
	}
	if _, exists := bridge["harness_config"]; exists {
		t.Fatal("public configuration crossed the private bridge")
	}
	applied := bridge["native_model_options"].(map[string]any)
	if applied["effort"] != "high" || applied["thinking"].(map[string]any)["budgetTokens"] != float64(1024) {
		t.Fatal("bridge lost immutable native configuration")
	}
	req.AgentOptions["harness_config"] = map[string]any{"env": map[string]any{"ANTHROPIC_BASE_URL": "bypass"}}
	if _, _, err = prepareConfiguration(config, req); err != harnessconfig.ErrHarnessConfig {
		t.Fatalf("provider override accepted: %v", err)
	}
}
