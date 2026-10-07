package codex

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent/clirunner"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentcapabilities"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentplugin"
	"github.com/MiniMax-AI/OpenAgentCore/internal/modelprovider"
)

func TestViewExecutorLaunchesInTheSessionView(t *testing.T) {
	const realKey = "sk-real-sentinel-key"
	t.Setenv("OAC_VIEW_SENTINEL", "from-test-process")
	t.Setenv("CODEX_API_KEY", realKey)
	declared := newView(filepath.Join(t.TempDir(), "codex"), false)
	info := Declaration.Info
	info.Available = true
	registry := agent.NewRegistry()
	registry.Register(Declaration, agent.Runtime{Info: info, View: &declared}, agent.EnvironmentSupport{Local: true, None: true})
	view, err := registry.ResolveView("codex")
	if err != nil {
		t.Fatal(err)
	}

	// A Harness from an earlier turn can leave links in the home it owns.
	home := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("outside\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(home, viewCodexHome), 0o700); err != nil {
		t.Fatal(err)
	}
	planted := filepath.Join(home, viewCodexHome, "config.toml")
	if err := os.Symlink(outside, planted); err != nil {
		t.Fatal(err)
	}
	var launched []clirunner.StartOptions
	session := agent.ViewSession{
		Home:  agent.ViewDir{Host: home, View: agent.ViewPrivateRoot + "/" + agent.ViewHomeName},
		Proxy: "http://127.0.0.1:17100",
		MCP:   []agent.MCPBinding{{ServerLabel: "docs", ConnectionOrigin: "service", CredentialAuthority: "project_vault", Transport: "http", ServerURL: "http://127.0.0.1:17102/mcp"}},
		Launch: func(opts clirunner.StartOptions) (*clirunner.Process, error) {
			launched = append(launched, opts)
			return nil, errors.New("recorded")
		},
	}
	req := proto.PromptRequestPayload{
		AgentStateKey:    "state",
		Model:            "m",
		ModelProvider:    &modelprovider.Provider{Protocol: modelprovider.Responses, BaseURL: "http://127.0.0.1:17101", APIKey: modelprovider.Placeholder},
		LocalEnvironment: &proto.LocalEnvironment{WorkspaceRoot: "/workspace", NetworkAccess: "enabled"},
	}
	if _, err := view.Executor(t.Context(), req, session); err == nil || len(launched) != 1 {
		t.Fatalf("launches %d, err %v", len(launched), err)
	}
	launch := launched[0]
	if !slices.Contains(view.LocalExec, launch.Binary) {
		t.Fatalf("binary %q is not in LocalExec %v", launch.Binary, view.LocalExec)
	}
	for _, entry := range launch.Env {
		if strings.HasPrefix(entry, "OAC_VIEW_SENTINEL=") {
			t.Fatal("the test process environment reached the Harness")
		}
	}
	if info, err := os.Lstat(planted); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("config.toml was not replaced: %v", err)
	}
	config, err := os.ReadFile(planted)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`base_url = "http://127.0.0.1:17101"` + "\n", `experimental_bearer_token = "` + modelprovider.Placeholder + `"`, `"http://127.0.0.1:17102/mcp"`} {
		if !strings.Contains(string(config), want) {
			t.Fatalf("config.toml lacks %s:\n%s", want, config)
		}
	}
	exposed := append(append([]string{launch.Binary}, launch.Args...), launch.Env...)
	err = filepath.WalkDir(home, func(name string, entry fs.DirEntry, err error) error {
		if err == nil && entry.Type().IsRegular() {
			body, readErr := os.ReadFile(name)
			exposed = append(exposed, string(body))
			err = readErr
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range exposed {
		if strings.Contains(text, realKey) {
			t.Fatal("the real key reached the view")
		}
	}
	args := strings.Join(launch.Args, " ")
	for _, override := range []string{"features.shell_snapshot=false", "allow_login_shell=false", "features.hooks=false", "features.plugins=false", "features.memories=false", "features.skill_mcp_dependency_install=false", "project_root_markers=[]"} {
		if !strings.Contains(args, "-c "+override) {
			t.Fatalf("missing -c %s in %s", override, args)
		}
	}

	if err := os.Remove(planted); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, planted); err != nil {
		t.Fatal(err)
	}
	if err := appendConfigTOML(filepath.Join(home, viewCodexHome), "x = 1\n"); err == nil {
		t.Fatal("a write followed a link out of the home")
	}
	if body, err := os.ReadFile(outside); err != nil || string(body) != "outside\n" {
		t.Fatalf("outside file changed: %q, %v", body, err)
	}

	session.MCP = []agent.MCPBinding{{ServerLabel: "local", ConnectionOrigin: "environment", CredentialAuthority: "none", Transport: "stdio", Stdio: &proto.EnvironmentMCP{
		Server: agentplugin.MCPServer{Name: "local", Type: "stdio", Command: agent.ViewAlias(0)}}}}
	req.LocalEnvironment, req.DisableExecutionEnvironment = nil, true
	if _, err := view.Executor(t.Context(), req, session); err == nil || len(launched) != 2 {
		t.Fatalf("environment none: launches %d, err %v", len(launched), err)
	}
	if launch := launched[1]; launch.Dir != "/.oac/home/work" || !slices.Contains(launch.Env, "CODEX_EXEC_SERVER_URL=none") {
		t.Fatalf("environment none launched in %q with %v", launch.Dir, launch.Env)
	}
	if config, err := os.ReadFile(planted); err != nil || !strings.Contains(string(config), "command = \"/.oac/bin/oac-mcp-0\"\n\n") {
		t.Fatalf("stdio alias config.toml: %v\n%s", err, config)
	}
}

// TestViewHandsCodexTheInstalledSkillAndMCP checks that a view registers the
// installed Skill's root at the sandbox path the owner filled, which Codex
// lists, and configures the installed stdio MCP server under its alias.
func TestViewHandsCodexTheInstalledSkillAndMCP(t *testing.T) {
	_, cfg, root := preparationFixture(t)
	declared := newView(filepath.Join(t.TempDir(), "codex"), false)
	home := t.TempDir()
	session := agent.ViewSession{
		Home:  agent.ViewDir{Host: home, View: agent.ViewPrivateRoot + "/" + agent.ViewHomeName},
		Proxy: "http://127.0.0.1:17100",
		MCP: []agent.MCPBinding{{ServerLabel: "local", ConnectionOrigin: "environment", CredentialAuthority: "none", Transport: "stdio", Stdio: &proto.EnvironmentMCP{
			Server: agentplugin.MCPServer{Name: "local", Type: "stdio", Command: agent.ViewAlias(0)}}}},
		// The fake Codex runs on the host, outside the view.
		Launch: func(opts clirunner.StartOptions) (*clirunner.Process, error) {
			opts.Binary, opts.Dir, opts.Env = cfg.codexBinary, "", os.Environ()
			return clirunner.Start(opts)
		},
	}
	server := map[string]any{"command": agent.ViewAlias(0), "args": []string{}, "environment_id": "local", "enabled": true, "default_tools_approval_mode": "approve"}
	config := filepath.Join(root, "mcp-config.json")
	writeMCPHTTPConfigResponse(t, config, map[string]any{"config": map[string]any{"mcp_servers": map[string]any{"local": server},
		"features": map[string]any{"plugins": false, "apps": false}, "mcp_oauth_credentials_store": "file"}})
	t.Setenv("OAC_TEST_PREPARATION_MCP_CONFIG", config)
	req := proto.PromptRequestPayload{AgentStateKey: "state", Model: "m",
		ModelProvider: &modelprovider.Provider{Protocol: modelprovider.Responses, BaseURL: "http://127.0.0.1:17101", APIKey: modelprovider.Placeholder},
		LocalEnvironment: &proto.LocalEnvironment{WorkspaceRoot: "/workspace", NetworkAccess: "enabled", CapabilityRoot: agentcapabilities.Directory,
			Skills: []agentcapabilities.InstalledSkill{{InstallationRoot: agentcapabilities.Directory, RelativeRoot: "skills/review", PackageRoot: "skills/review"}}}}
	e, err := declared.Executor(t.Context(), req, session)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := e.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	var registered SkillsExtraRootsSetParams
	for _, frame := range preparationFrames(t, root) {
		if frame.Method == "skills/extraRoots/set" && json.Unmarshal(frame.Params, &registered) != nil {
			t.Fatal(frame)
		}
	}
	if want := []string{agentcapabilities.Directory + "/skills/review"}; !slices.Equal(registered.ExtraRoots, want) {
		t.Errorf("extra roots %v, want %v", registered.ExtraRoots, want)
	}
	if body, err := os.ReadFile(filepath.Join(home, viewCodexHome, "config.toml")); err != nil || !strings.Contains(string(body), "command = \""+agent.ViewAlias(0)+"\"\n") {
		t.Errorf("config.toml: %v\n%s", err, body)
	}
}
