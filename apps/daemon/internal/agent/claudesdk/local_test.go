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

func TestLocalWorkspaceBindingAndRequiredHistory(t *testing.T) {
	config := workspaceFixture(t)
	config.Workspace.PublicDirectory = config.Workspace.Directory
	config.Workspace.NetworkAccess = "enabled"
	req := workspaceRequest()
	req.LocalEnvironment = &proto.LocalEnvironment{ID: "environment"}
	req.RequireExistingNativeSession = true
	bound := prepared(t, req)
	bound.WorkspaceRoot = config.Workspace.Directory
	start, _, err := prepareConfiguration(config, bound)
	if err != nil || !start.RequireHistory || start.Workspace.NetworkAccess != "enabled" {
		t.Fatal(start, err)
	}
	config.Workspace.PublicDirectory = config.Workspace.HomeDir
	if _, _, err := prepareConfiguration(config, bound); err == nil {
		t.Fatal("accepted a different public workspace")
	}
	alias := filepath.Join(filepath.Dir(config.Workspace.Directory), "alias")
	if err := os.Symlink(config.Workspace.Directory, alias); err != nil {
		t.Fatal(err)
	}
	config.Workspace.PublicDirectory = alias
	if _, _, err := prepareConfiguration(config, bound); err != nil {
		t.Fatal("same workspace alias rejected", err)
	}
}

func TestRestrictedWorkspacePolicyIsRejected(t *testing.T) {
	config := workspaceFixture(t)
	config.Workspace.NetworkAccess = "restricted"
	config.Workspace.AllowedDomains = []string{"api.example.com"}
	if _, _, err := prepareConfiguration(config, prepared(t, workspaceRequest())); err == nil {
		t.Fatal("Runtime must not promise inner network isolation")
	}
}

func TestWorkspaceProviderCredentialsReplaceAmbientSelection(t *testing.T) {
	config := workspaceFixture(t)
	original := slices.Clone(config.Env)
	req := workspaceRequest()
	req.ModelProvider = &modelprovider.Provider{Protocol: modelprovider.Anthropic, BaseURL: "https://provider.example/anthropic", APIKey: "selected-secret"}
	start, env, err := prepareConfiguration(config, prepared(t, req))
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
}
