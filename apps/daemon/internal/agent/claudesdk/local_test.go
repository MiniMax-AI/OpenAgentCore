package claudesdk

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

func TestLocalWorkspaceBindingAndRequiredHistory(t *testing.T) {
	config := workspaceFixture(t)
	req := workspaceRequest()
	req.LocalEnvironment = &proto.LocalEnvironment{ID: "environment", WorkspaceRoot: config.Workspace}
	req.RequireExistingNativeSession = true
	start, _, err := prepareConfiguration(config, req)
	if err != nil || !start.RequireHistory {
		t.Fatal(start, err)
	}
}

func TestWorkspaceProviderCredentialsReplaceAmbientSelection(t *testing.T) {
	config := workspaceFixture(t)
	original := slices.Clone(config.Env)
	req := workspaceRequest()
	req.AgentOptions["model_provider"] = map[string]any{"protocol": "anthropic", "base_url": "https://provider.example/anthropic", "api_key": "selected-secret", "context_window": 64000, "max_output_tokens": 4096}
	start, env, err := prepareConfiguration(config, req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(start)
	if strings.Contains(string(raw), "selected-secret") || !slices.Equal(original, config.Env) {
		t.Fatal("provider leaked or mutated shared configuration")
	}
	if !slices.Contains(env, "ANTHROPIC_AUTH_TOKEN=selected-secret") || slices.Contains(env, "ANTHROPIC_AUTH_TOKEN=selected-provider-fixture") {
		t.Fatal("provider selection was not exclusive")
	}
	if !slices.Contains(env, "CLAUDE_CODE_MAX_CONTEXT_TOKENS=64000") || !slices.Contains(env, "CLAUDE_CODE_MAX_OUTPUT_TOKENS=4096") {
		t.Fatal("token limits were not native")
	}
	for _, value := range []any{nil, "secret", map[string]any{"protocol": "anthropic", "base_url": "http://provider.example", "api_key": "secret"}, map[string]any{"protocol": "anthropic", "base_url": "https://user:pass@provider.example", "api_key": "secret"}} {
		req.AgentOptions["model_provider"] = value
		if _, _, err := prepareConfiguration(config, req); err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatal("unsafe provider accepted or disclosed")
		}
	}
}
