package claudesdk

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/modelprovider"
)

func TestLocalWorkspaceBindingNetworkAndRequiredHistory(t *testing.T) {
	config := workspaceFixture(t)
	config.Workspace.PublicDirectory = config.Workspace.Directory
	config.Workspace.NetworkAccess = "enabled"
	req := workspaceRequest()
	req.LocalEnvironment = &proto.LocalEnvironment{ID: "environment", NetworkAccess: "enabled", WorkspaceRoot: config.Workspace.Directory}
	req.RequireExistingNativeSession = true
	start, _, err := prepareConfiguration(config, req)
	if err != nil || !start.RequireHistory || start.Workspace.NetworkAccess != "enabled" {
		t.Fatal(start, err)
	}
	req.LocalEnvironment.NetworkAccess = "disabled"
	if _, _, err := prepareConfiguration(config, req); err == nil {
		t.Fatal("accepted different Runtime network policy")
	}
	req.LocalEnvironment.NetworkAccess = "enabled"
	config.Workspace.PublicDirectory = config.Workspace.HomeDir
	if _, _, err := prepareConfiguration(config, req); err == nil {
		t.Fatal("accepted a different public workspace")
	}
	alias := filepath.Join(filepath.Dir(config.Workspace.Directory), "alias")
	if err := os.Symlink(config.Workspace.Directory, alias); err != nil {
		t.Fatal(err)
	}
	config.Workspace.PublicDirectory = alias
	if _, _, err := prepareConfiguration(config, req); err != nil {
		t.Fatal("same workspace alias rejected", err)
	}

}

func TestRestrictedWorkspacePolicyUsesExactBoundAuthority(t *testing.T) {
	config := workspaceFixture(t)
	config.Workspace.NetworkAccess = "restricted"
	config.Workspace.AllowedDomains = []string{"Example.com", "api.example.com", "example.com"}
	req := workspaceRequest()
	req.LocalEnvironment = &proto.LocalEnvironment{ID: "environment", NetworkAccess: "restricted", AllowedDomains: []string{"api.example.com", "EXAMPLE.COM"}, WorkspaceRoot: config.Workspace.Directory}
	if _, _, err := prepareConfiguration(config, req); err == nil {
		t.Fatal("Runtime must not promise inner network isolation")
	}
	for _, domains := range [][]string{{"example.com"}, {"example.org"}, nil} {
		req.LocalEnvironment.AllowedDomains = domains
		if _, _, err := prepareConfiguration(config, req); err == nil {
			t.Fatal("different policy entered bound Runtime", domains)
		}
	}
}

func TestWorkspaceProviderCredentialsReplaceAmbientSelection(t *testing.T) {
	config := workspaceFixture(t)
	original := slices.Clone(config.Env)
	req := workspaceRequest()
	req.ModelProvider = &modelprovider.Provider{Protocol: modelprovider.Anthropic, BaseURL: "https://provider.example/anthropic", APIKey: "selected-secret"}
	start, env, err := prepareConfiguration(config, req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(start)
	if strings.Contains(string(raw), "selected-secret") || !slices.Equal(original, config.Env) {
		t.Fatal("provider leaked or mutated shared configuration")
	}
	if !slices.Contains(env, "ANTHROPIC_API_KEY=selected-secret") || slices.ContainsFunc(env, func(entry string) bool { return strings.HasPrefix(entry, "ANTHROPIC_AUTH_TOKEN=") }) {
		t.Fatal("provider selection was not exclusive")
	}
	for _, baseURL := range []string{"http://provider.example", "https://user:pass@provider.example"} {
		req.ModelProvider = &modelprovider.Provider{Protocol: modelprovider.Anthropic, BaseURL: baseURL, APIKey: "secret"}
		if _, _, err := prepareConfiguration(config, req); err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatal("unsafe provider accepted or disclosed")
		}
	}
}
