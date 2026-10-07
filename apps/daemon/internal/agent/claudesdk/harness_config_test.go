package claudesdk

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig"
)

func TestHarnessConfigReachesBridge(t *testing.T) {
	root := t.TempDir()
	t.Setenv("OAC_RUNTIME_HOME", root)
	config := Config{Entrypoint: filepath.Join(root, "main.js"), StateDir: filepath.Join(root, "state")}
	req := proto.PromptRequestPayload{Model: "fixture", HarnessConfig: proto.HarnessConfig(`{"effort":"high","thinking":{"type":"enabled","budgetTokens":1024}}`)}
	start, _, err := prepareConfiguration(config, req)
	if err != nil {
		t.Fatal(err)
	}
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
		t.Fatal("bridge lost native configuration")
	}
	req.HarnessConfig = proto.HarnessConfig(`{"env":{"ANTHROPIC_BASE_URL":"bypass"}}`)
	if _, _, err = prepareConfiguration(config, req); err != harnessconfig.ErrHarnessConfig {
		t.Fatalf("provider override accepted: %v", err)
	}
}
