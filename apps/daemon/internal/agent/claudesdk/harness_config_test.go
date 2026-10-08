package claudesdk

import (
	"encoding/json"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

func TestHarnessConfigReachesBridge(t *testing.T) {
	req := proto.PromptRequestPayload{ModelProvider: fixtureProvider(), Model: "fixture", HarnessConfig: proto.HarnessConfig(`{"effort":"high","thinking":{"type":"enabled","budgetTokens":1024}}`)}
	start, _, err := prepareOptions(prepared(t, req))
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
}
