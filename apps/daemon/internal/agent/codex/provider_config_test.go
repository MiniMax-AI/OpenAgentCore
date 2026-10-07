package codex

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/modelprovider"
)

func TestWriteCodexProviderConfig_Minimal(t *testing.T) {
	dir := t.TempDir()
	cfg := providerConfig{
		BaseURL:     "https://platform-api.example.com/v1",
		BearerToken: "sk-test",
	}
	if err := writeCodexProviderConfig(dir, cfg); err != nil {
		t.Fatalf("write: %v", err)
	}
	body := mustReadFile(t, filepath.Join(dir, "config.toml"))
	for _, want := range []string{
		`[model_providers.oac]`,
		`name = "OpenAgentCore"`,
		`base_url = "https://platform-api.example.com/v1"`,
		`experimental_bearer_token = "sk-test"`,
		`wire_api = "responses"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("config.toml missing %q\n---\n%s", want, body)
		}
	}
}

// TestWriteCodexProviderConfig_PinsResponsesWire confirms wire_api stays
// "responses" even when the caller forgot to set it. codex-rs removed
// the "chat" variant; emitting empty would fall back to upstream's
// default which today is "responses" but could drift.
func TestWriteCodexProviderConfig_PinsResponsesWire(t *testing.T) {
	dir := t.TempDir()
	cfg := providerConfig{
		BaseURL:     "https://x/v1",
		BearerToken: "sk-x",
		// WireAPI deliberately empty
	}
	if err := writeCodexProviderConfig(dir, cfg); err != nil {
		t.Fatalf("write: %v", err)
	}
	body := mustReadFile(t, filepath.Join(dir, "config.toml"))
	if !strings.Contains(body, `wire_api = "responses"`) {
		t.Fatalf("wire_api default not pinned: %s", body)
	}
}

func TestWriteCodexProviderConfig_FullProvider(t *testing.T) {
	dir := t.TempDir()
	cfg := providerConfig{
		Name:        "mygw",
		BaseURL:     "https://platform-api.example.com/v1",
		BearerToken: "sk-test-fixture",
		WireAPI:     "responses",
		HTTPHeaders: map[string]string{
			"X-Sub-Module": "codex-internal",
			"X-Request-ID": "abc",
		},
		QueryParams: map[string]string{
			"api-version": "2025-04-01-preview",
		},
		RequestMaxRetries: 4,
		StreamMaxRetries:  3,
	}
	if err := writeCodexProviderConfig(dir, cfg); err != nil {
		t.Fatalf("write: %v", err)
	}
	body := mustReadFile(t, filepath.Join(dir, "config.toml"))
	for _, want := range []string{
		`name = "mygw"`,
		`base_url = "https://platform-api.example.com/v1"`,
		`experimental_bearer_token = "sk-test-fixture"`,
		`request_max_retries = 4`,
		`stream_max_retries = 3`,
		`[model_providers.oac.http_headers]`,
		`"X-Sub-Module" = "codex-internal"`,
		`"X-Request-ID" = "abc"`,
		`[model_providers.oac.query_params]`,
		`"api-version" = "2025-04-01-preview"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("config.toml missing %q\n---\n%s", want, body)
		}
	}
}

func TestWriteCodexProviderConfig_RejectsMissingFields(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name string
		cfg  providerConfig
	}{
		{"missing base_url", providerConfig{BearerToken: "sk-x"}},
		{"missing bearer_token", providerConfig{BaseURL: "https://x"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := writeCodexProviderConfig(dir, tc.cfg); err == nil {
				t.Fatal("expected error for incomplete provider config")
			}
		})
	}
}

// TestWriteCodexProviderConfig_AppendsAlongsideMCP verifies the two
// writers coexist on the same file. Without append semantics one would
// silently overwrite the other depending on call order.
func TestWriteCodexProviderConfig_AppendsAlongsideMCP(t *testing.T) {
	dir := t.TempDir()
	mcpCfg := map[string]mcpServerConfig{
		"docs": {Name: "docs", Command: "docs-server"},
	}
	if err := writeCodexMCPConfig(dir, mcpCfg); err != nil {
		t.Fatalf("mcp write: %v", err)
	}
	if err := writeCodexProviderConfig(dir, providerConfig{
		BaseURL: "https://x", BearerToken: "sk-x",
	}); err != nil {
		t.Fatalf("provider write: %v", err)
	}
	body := mustReadFile(t, filepath.Join(dir, "config.toml"))
	if !strings.Contains(body, `[mcp_servers."docs"]`) {
		t.Errorf("mcp_servers block lost after provider write:\n%s", body)
	}
	if !strings.Contains(body, `[model_providers.oac]`) {
		t.Errorf("model_providers block missing:\n%s", body)
	}
}

func TestBuildSessionPlan_PinsModelProviderWhenProviderSet(t *testing.T) {
	plan, err := BuildSessionPlan(proto.PromptRequestPayload{
		RunID: "run-x", AgentStateKey: "conv-1/agent-1/codex", Model: "fixture-model",
		ModelProvider: &modelprovider.Provider{Protocol: modelprovider.Responses, BaseURL: "https://x/v1", APIKey: "sk-x"},
	})
	if err != nil {
		t.Fatalf("BuildSessionPlan: %v", err)
	}
	defer plan.Cleanup()
	if plan.ModelProvider != "oac" {
		t.Fatalf("plan.ModelProvider = %q, want oac", plan.ModelProvider)
	}
	found := false
	for _, kv := range plan.ExtraConfig {
		if kv[0] == "model_provider" && kv[1] == `"oac"` {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("ExtraConfig missing model_provider override: %+v", plan.ExtraConfig)
	}
	// And the config.toml was actually written.
	codexHome := ""
	for _, kv := range plan.Env {
		if strings.HasPrefix(kv, "CODEX_HOME=") {
			codexHome = strings.TrimPrefix(kv, "CODEX_HOME=")
		}
	}
	body := mustReadFile(t, filepath.Join(codexHome, "config.toml"))
	if !strings.Contains(body, `[model_providers.oac]`) {
		t.Fatalf("config.toml missing provider block:\n%s", body)
	}
}

func mustReadFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}
