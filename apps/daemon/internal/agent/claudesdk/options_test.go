package claudesdk

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/modelprovider"
)

// Device credentials never stand in for the prepared provider.
func TestPreparedModelAndProviderReachTheBridge(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "device-key")
	for _, prompt := range []string{"", "instructions"} {
		root := t.TempDir()
		t.Setenv("OAC_RUNTIME_HOME", root)
		config := Config{Entrypoint: filepath.Join(root, "main.js"), StateDir: filepath.Join(root, "state")}
		start, env, err := prepareConfiguration(config, prepared(t, proto.PromptRequestPayload{ModelProvider: fixtureProvider(), Model: "test-model", SystemPrompt: prompt}))
		if err != nil || start.Model != "test-model" || start.SystemPrompt != prompt || !slices.Contains(env, "ANTHROPIC_API_KEY=fixture-key") {
			t.Fatalf("model %q, system prompt %q, error %v", start.Model, start.SystemPrompt, err)
		}
	}
}

// prepared is req as the registry hands it to the factory.
func prepared(t testing.TB, req proto.PromptRequestPayload) agent.PrepareRequest {
	t.Helper()
	configuration, err := Declaration.Configuration.Prepare(req)
	if err != nil {
		t.Fatal(err)
	}
	return agent.PrepareRequest{PromptRequestPayload: req, Prepared: configuration}
}

// fixtureProvider is the provider every Claude request carries.
func fixtureProvider() *modelprovider.Provider {
	return &modelprovider.Provider{Protocol: modelprovider.Anthropic, BaseURL: "https://model.example", APIKey: "fixture-key"}
}
