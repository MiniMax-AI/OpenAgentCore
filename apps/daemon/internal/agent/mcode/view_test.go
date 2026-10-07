package mcode

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent/clirunner"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent/viewloader"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentcapabilities"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentplugin"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentskill"
	"github.com/MiniMax-AI/OpenAgentCore/internal/modelprovider"
)

const viewRealKey = "sk-view-real-key-sentinel"

// viewFixture registers a view over a fake install and returns it with a
// request as the agent host hands it over.
func viewFixture(t *testing.T) (viewInstall, agent.View, agent.PrepareRequest) {
	t.Helper()
	harness, bin, libs := t.TempDir(), t.TempDir(), t.TempDir()
	for _, file := range []string{"bridge.mjs", "native/cli.js", "native/assets/skills/.keep", "native/assets/agents/.keep", filepath.Join(bin, "node")} {
		if !filepath.IsAbs(file) {
			file = filepath.Join(harness, file)
		}
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, nil, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	lib := agent.ViewMount{Name: viewloader.MountName, HostDir: libs}
	loader := viewloader.Fragment{Closure: []agent.ViewMount{lib}, LibraryPath: lib.Path(),
		Overlays: []agent.ViewOverlay{{Path: "/lib64/ld-linux-x86-64.so.2", Source: filepath.Join(libs, "ld.so"), Exec: true}}}
	install, err := newViewInstall(filepath.Join(bin, "node"), filepath.Join(harness, "native/cli.js"), filepath.Join(harness, "bridge.mjs"), loader)
	if err != nil {
		t.Fatal(err)
	}
	declared := install.view()
	registry := agent.NewRegistry()
	info := Declaration.Info
	info.Available = true
	registry.Register(Declaration, agent.Runtime{Info: info, View: &declared}, agent.EnvironmentSupport{Local: true, None: true})
	view, err := registry.ResolveView("mcode")
	if err != nil {
		t.Fatal(err)
	}

	t.Setenv("OAC_TEST_VIEW_SENTINEL", "daemon-only")
	t.Setenv("ANTHROPIC_API_KEY", viewRealKey)
	req := testRequest(t)
	req.DisableExecutionEnvironment = false
	req.LocalEnvironment = &proto.LocalEnvironment{}
	req.ModelProvider = &modelprovider.Provider{Protocol: modelprovider.Anthropic, BaseURL: "http://127.0.0.1:4101", APIKey: modelprovider.Placeholder, ContextWindow: 64000, MaxOutputTokens: 4096}
	bound := prepared(t, req)
	bound.WorkspaceRoot = "/workspace"
	return install, view, bound
}

func viewSession(launched *clirunner.StartOptions) agent.ViewSession {
	return agent.ViewSession{Launch: func(options clirunner.StartOptions) (*clirunner.Process, error) {
		*launched = options
		return nil, errors.New("launch recorded")
	}}
}

func TestViewLaunchesNodeWithGatewayOnly(t *testing.T) {
	install, view, req := viewFixture(t)
	var launched clirunner.StartOptions
	session := viewSession(&launched)
	session.Home = agent.ViewDir{Host: t.TempDir(), View: path.Join(agent.ViewPrivateRoot, agent.ViewHomeName)}
	if _, err := view.Executor(t.Context(), req, session); err == nil || err.Error() != "launch recorded" {
		t.Fatalf("Executor = %v", err)
	}

	if !slices.Contains(view.LocalExec, launched.Binary) || !slices.Equal(launched.Args, []string{install.cli, "acp"}) || path.Base(install.cli) != "cli.js" || launched.Dir != "/workspace" {
		t.Fatalf("launched %s %q in %s", launched.Binary, launched.Args, launched.Dir)
	}
	env := strings.Join(launched.Env, "\n")
	if strings.Contains(env, "OAC_TEST_VIEW_SENTINEL") || strings.Contains(env, viewRealKey) || !slices.Contains(launched.Env, "HOME="+path.Join(session.Home.View, viewDataName)) ||
		!slices.Contains(launched.Env, "LD_LIBRARY_PATH="+install.loader.LibraryPath) {
		t.Fatalf("environment is not closed: %q", launched.Env)
	}
	config, err := os.ReadFile(filepath.Join(session.Home.Host, viewDataName, "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var native struct {
		Provider struct {
			OAC struct {
				Options struct{ BaseURL, APIKey string }
			} `json:"oac"`
		} `json:"custom_provider"`
	}
	if err := json.Unmarshal(config, &native); err != nil || native.Provider.OAC.Options.BaseURL != "http://127.0.0.1:4101" || native.Provider.OAC.Options.APIKey != modelprovider.Placeholder {
		t.Fatalf("native provider = %+v (%v)", native.Provider.OAC.Options, err)
	}
	profile, err := os.ReadFile(filepath.Join(session.Home.Host, viewDataName, "workspace-profile.json"))
	if err != nil || strings.Contains(string(profile), "toolEnvFile") {
		t.Fatalf("profile = %s (%v)", profile, err)
	}
	err = filepath.WalkDir(session.Home.Host, func(file string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		if raw, err := os.ReadFile(file); err != nil || strings.Contains(string(raw), viewRealKey) {
			t.Errorf("%s holds the real key (%v)", file, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// With environment none the CLI runs in the work directory without the
// workspace tools. In the workspace it runs each stdio binding's alias
// without arguments and loads each installed Skill from its sandbox path.
func TestViewRunsEnvironmentNoneStdioAliasesAndSkills(t *testing.T) {
	install, _, req := viewFixture(t)
	session := agent.ViewSession{Home: agent.ViewDir{Host: t.TempDir(), View: path.Join(agent.ViewPrivateRoot, agent.ViewHomeName)}}
	none := req
	none.LocalEnvironment, none.DisableExecutionEnvironment, none.WorkspaceRoot = nil, true, ""
	opts, err := install.prepare(none, session)
	if err != nil || opts.Dir != "/.oac/home/work" || opts.MCP == nil || len(opts.MCP) != 0 {
		t.Fatalf("environment none runs in %q with MCP %v: %v", opts.Dir, opts.MCP, err)
	}

	session.MCP = []agent.MCPBinding{{ServerLabel: "local", ConnectionOrigin: "environment", CredentialAuthority: "none", Transport: "stdio", Stdio: &agent.EnvironmentMCP{
		Server: agentplugin.MCPServer{Name: "local", Type: "stdio", Command: agent.ViewAlias(0)}}}}
	req.CapabilityRoot, req.Skills = agentcapabilities.Directory, []agentcapabilities.InstalledSkill{{InstallationRoot: agentcapabilities.Directory,
		Metadata: agentskill.Metadata{Type: "inline", Name: "review", Description: "Review."}, RelativeRoot: "skills/review", PackageRoot: "skills/review"}}
	if opts, err = install.prepare(req, session); err != nil || len(opts.MCP) != 2 || opts.MCP[0]["name"] != "oac_workspace" || opts.MCP[1]["command"] != agent.ViewAlias(0) {
		t.Fatalf("stdio MCP = %v: %v", opts.MCP, err)
	}
	if args, ok := opts.MCP[1]["args"].([]string); !ok || args == nil || len(args) != 0 {
		t.Fatalf("the stdio alias runs with arguments %#v", opts.MCP[1]["args"])
	}
	if target, err := os.Readlink(filepath.Join(session.Home.Host, viewDataName, "skills", "review")); err != nil || target != agentcapabilities.Directory+"/skills/review" {
		t.Fatalf("the native Skill links to %q: %v", target, err)
	}
	var config struct {
		Agents map[string]struct{ Skills, Tools []string }
	}
	raw, err := os.ReadFile(filepath.Join(session.Home.Host, viewDataName, "config.yaml"))
	if err != nil || json.Unmarshal(raw, &config) != nil || !slices.Equal(config.Agents["default"].Skills, []string{"review"}) || !slices.Contains(config.Agents["default"].Tools, "skill") {
		t.Fatalf("native configuration %s: %v", raw, err)
	}
}

func TestViewReadsSubagentsBesideTheCLI(t *testing.T) {
	install, _, req := viewFixture(t)
	req.DisableSubagents, req.MaxConcurrentSubagents = false, new(2)
	var launched, spawned clirunner.StartOptions
	session := viewSession(&launched)
	session.Home = agent.ViewDir{Host: t.TempDir(), View: path.Join(agent.ViewPrivateRoot, agent.ViewHomeName)}
	session.Spawn = func(options clirunner.StartOptions) (*clirunner.Process, error) {
		spawned = options
		return clirunner.Start(clirunner.StartOptions{Parent: options.Parent, Binary: "/bin/echo", Args: []string{`{"version":1,"complete":true,"rootSessionId":"root"}`}})
	}
	opts, err := install.prepare(req, session)
	if err != nil {
		t.Fatal(err)
	}
	s := &Session{sessionID: "root", opts: opts}
	if _, err := s.readSubagents(t.Context()); err != nil {
		t.Fatal(err)
	}
	data := path.Join(session.Home.View, viewDataName)
	args := []string{"--disable-warning=ExperimentalWarning", path.Join(path.Dir(install.bridge), "subagent-snapshot.mjs"), data, "root"}
	if spawned.Binary != install.node || !slices.Equal(spawned.Args, args) || spawned.Dir != data || !slices.Contains(spawned.Env, "LD_LIBRARY_PATH="+install.loader.LibraryPath) {
		t.Fatalf("spawned %+v", spawned)
	}
}

func TestViewDoesNotFollowHomeLinks(t *testing.T) {
	_, view, req := viewFixture(t)
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("unchanged"), 0o600); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, viewDataName), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(home, viewDataName, "config.yaml")); err != nil {
		t.Fatal(err)
	}
	var launched clirunner.StartOptions
	session := viewSession(&launched)
	session.Home = agent.ViewDir{Host: home, View: path.Join(agent.ViewPrivateRoot, agent.ViewHomeName)}
	if _, err := view.Executor(t.Context(), req, session); err == nil || launched.Binary != "" {
		t.Fatalf("Executor = %v, launched %q", err, launched.Binary)
	}
	if raw, err := os.ReadFile(outside); err != nil || string(raw) != "unchanged" {
		t.Fatalf("outside file = %q (%v)", raw, err)
	}
}
