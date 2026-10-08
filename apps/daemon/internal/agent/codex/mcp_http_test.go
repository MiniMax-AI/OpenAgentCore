package codex

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

func TestPublicMCPHTTPPlanOwnsConfigurationAndPreservesHistory(t *testing.T) {
	root := t.TempDir()
	cfg := testView(t, "", root)
	tools := []string{"lookup.docs", `quote"tool`}
	denyAll := []string{}
	cfg.view.MCP = []agent.MCPBinding{
		{ServerLabel: "docs.server", ConnectionOrigin: "service", CredentialAuthority: "project_vault", Transport: "http", ServerURL: "http://127.0.0.1:17102/mcp", AllowedTools: &tools, Required: true},
		{ServerLabel: "blocked", ConnectionOrigin: "service", CredentialAuthority: "project_vault", Transport: "http", ServerURL: "http://127.0.0.1:17103/mcp", AllowedTools: &denyAll},
	}
	req := prepared(t, "public-mcp", proto.PromptRequestPayload{Model: "fixture", ModelProvider: fixtureProvider(), DisableExecutionEnvironment: true})
	plan, err := prepareViewPlan(t.Context(), req, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer plan.Cleanup()
	if !slices.Contains(plan.DisableFeatures, "apps") || !slices.Contains(plan.DisableFeatures, "plugins") || !slices.Contains(plan.ExtraConfig, [2]string{"mcp_oauth_credentials_store", `"file"`}) {
		t.Fatal("native profile was not pinned")
	}
	if plan.Cwd != filepath.Join(root, agent.ViewWorkName) {
		t.Fatal("environment:none cwd is not the view's work directory", plan.Cwd)
	}
	home := filepath.Join(root, viewCodexHome)
	config, err := os.ReadFile(filepath.Join(home, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{`[mcp_servers."docs.server"]`, `enabled_tools = ["lookup.docs", "quote\"tool"]`, `enabled_tools = []`, "required = true"} {
		if !strings.Contains(string(config), expected) {
			t.Fatalf("missing native config %q", expected)
		}
	}
	history := filepath.Join(home, "retained-history.jsonl")
	if err := os.WriteFile(history, []byte("native-history"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg.view.MCP = []agent.MCPBinding{{ServerLabel: "replacement", ConnectionOrigin: "service", CredentialAuthority: "project_vault", Transport: "http", ServerURL: "http://127.0.0.1:17104/mcp"}}
	second, err := prepareViewPlan(t.Context(), req, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Cleanup()
	config, err = os.ReadFile(filepath.Join(home, "config.toml"))
	if err != nil || strings.Contains(string(config), "docs.server") || !strings.Contains(string(config), "replacement") || strings.Contains(string(config), "enabled_tools") || strings.Contains(string(config), "required") {
		t.Fatal("cold configuration retained old servers or changed unrestricted tools", err)
	}
	retained, err := os.ReadFile(history)
	if err != nil || string(retained) != "native-history" {
		t.Fatal("configuration reset changed native history", err)
	}
}

func TestPublicMCPHTTPRejectsStoredCredentials(t *testing.T) {
	valid := []proto.MCPHTTPServer{{ConnectionOrigin: "service", ServerLabel: "docs", ServerURL: "https://docs.example/mcp"}}
	root := t.TempDir()
	cfg := testView(t, "", root)
	cfg.view.MCP = []agent.MCPBinding{{ServerLabel: "docs", ConnectionOrigin: "service", CredentialAuthority: "project_vault", Transport: "http", ServerURL: "http://127.0.0.1:17102/mcp"}}
	if err := os.Mkdir(filepath.Join(root, viewCodexHome), 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, viewCodexHome, ".credentials.json")
	stored := []byte(`{"synthetic":"private"}`)
	if err := os.WriteFile(path, stored, 0o600); err != nil {
		t.Fatal(err)
	}
	req := prepared(t, "credentials", proto.PromptRequestPayload{Model: "fixture", ModelProvider: fixtureProvider(), DisableExecutionEnvironment: true, MCPHTTPServers: &valid})
	if _, err := prepareViewPlan(t.Context(), req, cfg); err == nil {
		t.Fatal("existing MCP credentials accepted")
	}
	if after, err := os.ReadFile(path); err != nil || !reflect.DeepEqual(after, stored) {
		t.Fatal("existing credentials were modified", err)
	}
}

// This is the pinned native serializer's config/read shape, not live discovery.
func mcpHTTPConfigResponse(servers map[string]mcpServerConfig) map[string]any {
	entries := make(map[string]any, len(servers))
	for name, server := range servers {
		entry := map[string]any{"url": server.URL, "environment_id": "local", "enabled": true, "tool_timeout_sec": nil, "required": server.Required}
		if server.EnabledTools != nil {
			entry["enabled_tools"] = append([]string{}, (*server.EnabledTools)...)
		}
		entries[name] = entry
	}
	return map[string]any{"config": map[string]any{"mcp_servers": entries, "features": map[string]any{"plugins": false, "apps": false}, "mcp_oauth_credentials_store": "file"}}
}

func writeMCPHTTPConfigResponse(t *testing.T, path string, response any) {
	t.Helper()
	data, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}
