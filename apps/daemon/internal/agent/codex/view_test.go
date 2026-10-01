package codex

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent/clirunner"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
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
	registry.Register(Declaration, agent.Runtime{Info: info, Session: Factory, View: &declared})
	view, err := registry.ResolveView("codex")
	if err != nil {
		t.Fatal(err)
	}

	home := t.TempDir()
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
		AgentOptions:     map[string]any{"model": "m", "model_provider": map[string]any{"protocol": "responses", "base_url": "http://127.0.0.1:17101", "api_key": modelprovider.Placeholder}},
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
	config, err := os.ReadFile(filepath.Join(home, viewCodexHome, "config.toml"))
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

	session.MCP = []agent.MCPBinding{{ServerLabel: "local", ConnectionOrigin: "environment", CredentialAuthority: "none", Transport: "stdio", Stdio: &proto.EnvironmentMCP{}}}
	if _, err := view.Executor(t.Context(), req, session); !errors.Is(err, agent.ErrUnsupportedOperation) || len(launched) != 1 {
		t.Fatalf("stdio MCP: launches %d, err %v", len(launched), err)
	}
}
