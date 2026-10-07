package claudesdk

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig"
	"github.com/MiniMax-AI/OpenAgentCore/internal/modelprovider"
)

// A request needs a model and a provider: device credentials never stand in.
func TestModelAndProviderAreRequired(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "device-key")
	for _, tc := range []struct {
		name string
		req  proto.PromptRequestPayload
		want string
		err  error
	}{
		{"no system prompt", proto.PromptRequestPayload{ModelProvider: fixtureProvider(), Model: "test-model"}, "", nil},
		{"system prompt", proto.PromptRequestPayload{ModelProvider: fixtureProvider(), Model: "test-model", SystemPrompt: "instructions"}, "instructions", nil},
		{"no model", proto.PromptRequestPayload{ModelProvider: fixtureProvider(), SystemPrompt: "instructions"}, "", harnessconfig.ErrModel},
		{"no provider", proto.PromptRequestPayload{Model: "test-model"}, "", harnessconfig.ErrModelProvider},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("OAC_RUNTIME_HOME", root)
			config := Config{Entrypoint: filepath.Join(root, "main.js"), StateDir: filepath.Join(root, "state")}
			tc.req.RunID, tc.req.Input = "run", proto.TextInput("hello")
			start, _, err := prepare(config, tc.req)
			if !errors.Is(err, tc.err) || (err == nil) != (tc.err == nil) || start.SystemPrompt != tc.want {
				t.Fatalf("system prompt %q, error %v", start.SystemPrompt, err)
			}
			if _, statErr := os.Stat(config.StateDir); tc.err != nil && !os.IsNotExist(statErr) {
				t.Fatal("rejected request created native state")
			}
		})
	}
}

// fixtureProvider is the provider every Claude request carries.
func fixtureProvider() *modelprovider.Provider {
	return &modelprovider.Provider{Protocol: modelprovider.Anthropic, BaseURL: "https://model.example", APIKey: "fixture-key"}
}
