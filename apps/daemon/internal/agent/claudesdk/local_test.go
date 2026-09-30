package claudesdk

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

func TestLocalWorkspaceBindingNetworkAndRequiredHistory(t *testing.T) {
	config := workspaceFixture(t)
	config.Workspace.PublicDirectory = config.Workspace.Directory
	config.Workspace.NetworkAccess = "enabled"
	req := workspaceRequest()
	req.LocalEnvironment = &proto.LocalEnvironment{ID: "environment", NetworkAccess: "enabled"}
	req.RequireExistingNativeSession = true
	start, _, err := prepare(config, req)
	if err != nil || !start.RequireHistory || start.Workspace.NetworkAccess != "enabled" {
		t.Fatal(start, err)
	}
	req.LocalEnvironment.NetworkAccess = "disabled"
	if _, _, err := prepare(config, req); err == nil {
		t.Fatal("accepted different Runtime network policy")
	}
	req.LocalEnvironment.NetworkAccess = "enabled"
	config.Workspace.PublicDirectory = config.Workspace.HomeDir
	if _, _, err := prepare(config, req); err == nil {
		t.Fatal("accepted a different public workspace")
	}
	alias := filepath.Join(filepath.Dir(config.Workspace.Directory), "alias")
	if err := os.Symlink(config.Workspace.Directory, alias); err != nil {
		t.Fatal(err)
	}
	config.Workspace.PublicDirectory = alias
	if _, _, err := prepare(config, req); err != nil {
		t.Fatal("same workspace alias rejected", err)
	}

}

func TestRestrictedWorkspacePolicyUsesExactBoundAuthority(t *testing.T) {
	config := workspaceFixture(t)
	config.Workspace.NetworkAccess = "restricted"
	config.Workspace.AllowedDomains = []string{"Example.com", "api.example.com", "example.com"}
	req := workspaceRequest()
	req.LocalEnvironment = &proto.LocalEnvironment{ID: "environment", NetworkAccess: "restricted", AllowedDomains: []string{"api.example.com", "EXAMPLE.COM"}}
	if _, _, err := prepare(config, req); err == nil {
		t.Fatal("Runtime must not promise inner network isolation")
	}
	for _, domains := range [][]string{{"example.com"}, {"example.org"}, nil} {
		req.LocalEnvironment.AllowedDomains = domains
		if _, _, err := prepare(config, req); err == nil {
			t.Fatal("different policy entered bound Runtime", domains)
		}
	}
}

func TestWorkspaceProviderCredentialsReplaceAmbientSelection(t *testing.T) {
	config := workspaceFixture(t)
	original := slices.Clone(config.Env)
	req := workspaceRequest()
	req.AgentOptions["model_provider"] = map[string]any{"protocol": "anthropic", "base_url": "https://provider.example/anthropic", "api_key": "selected-secret"}
	start, env, err := prepare(config, req)
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
	for _, value := range []any{nil, "secret", map[string]any{"protocol": "anthropic", "base_url": "http://provider.example", "api_key": "secret"}, map[string]any{"protocol": "anthropic", "base_url": "https://user:pass@provider.example", "api_key": "secret"}} {
		req.AgentOptions["model_provider"] = value
		if _, _, err := prepare(config, req); err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatal("unsafe provider accepted or disclosed")
		}
	}
}
