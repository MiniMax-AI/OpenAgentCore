package claudesdk

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	harnessconfiguration "github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig/claudesdk"
)

func workspaceFixture(t *testing.T) Config {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("OAC_RUNTIME_HOME", root)
	for _, name := range []string{"workspace", "state", "runtime-home", "secrets", "bin", "runtime/dist", "runtime/node_modules"} {
		if err := os.MkdirAll(filepath.Join(root, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	config := Config{Node: filepath.Join(root, "bin", "node"), Entrypoint: filepath.Join(root, "runtime", "dist", "main.js"), StateRoot: filepath.Join(root, "state"),
		Env:         []string{"ANTHROPIC_AUTH_TOKEN=selected-provider-fixture", "ANTHROPIC_BASE_URL=https://example.invalid"},
		RuntimeRoot: filepath.Join(root, "runtime-home"), Workspace: filepath.Join(root, "workspace")}
	for _, name := range []string{config.Node, config.Entrypoint, filepath.Join(filepath.Dir(config.Entrypoint), "runtime_check.js")} {
		if err := os.WriteFile(name, nil, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return config
}

func workspaceRequest() proto.PromptRequestPayload {
	return proto.PromptRequestPayload{AgentStateKey: testStateKey, RunID: "run", Input: proto.TextInput("hello"), DisableSubagents: true,
		Turn: proto.TurnSettings{Model: "fixture"}, AgentOptions: map[string]any{}}
}

func TestWorkspaceTrustedBindingAndEnvironment(t *testing.T) {
	config := workspaceFixture(t)
	t.Setenv("OAC_TEST_PARENT_SECRET", "parent-only")
	t.Setenv("ANTHROPIC_API_KEY", "unselected-provider")
	start, env, err := prepareConfiguration(config, workspaceRequest())
	if err != nil {
		t.Fatal(err)
	}
	if start.Cwd != config.Workspace || start.Workspace == nil || start.MCPHTTPServers != nil {
		t.Fatal("trusted binding was not retained")
	}
	raw, _ := json.Marshal(start)
	if strings.Contains(string(raw), "selected-provider-fixture") || strings.Contains(string(raw), "parent-only") {
		t.Fatal("secret value entered the private request")
	}
	values := map[string]string{}
	for _, item := range env {
		name, value, _ := strings.Cut(item, "=")
		values[name] = value
	}
	if values["OAC_TEST_PARENT_SECRET"] != "parent-only" {
		t.Fatal("lost user environment")
	}
	if _, ok := values["ANTHROPIC_API_KEY"]; ok {
		t.Fatal("inherited unselected provider credential")
	}
	if values["CLAUDE_CONFIG_DIR"] != testState(config) || values["ANTHROPIC_AUTH_TOKEN"] != "selected-provider-fixture" {
		t.Fatal("explicit runtime environment was not preserved")
	}
	entries, err := os.ReadDir(testState(config))
	if err != nil || len(entries) != 0 {
		t.Fatal("preparation created history or nested scratch")
	}
}

func TestSessionsKeepNativeStateInOwnDirectories(t *testing.T) {
	config := workspaceFixture(t)
	req := workspaceRequest()
	seen := map[string]string{}
	for _, key := range []string{"session-a", "session-b"} {
		req.AgentStateKey = key
		_, env, err := prepareConfiguration(config, req)
		if err != nil {
			t.Fatal(err)
		}
		state, _ := agent.StateDir(config.StateRoot, harnessconfiguration.Declaration.Kind, key)
		runtime, _ := agent.StateDir(config.RuntimeRoot, harnessconfiguration.Declaration.Kind, key)
		for name, want := range map[string]string{"CLAUDE_CONFIG_DIR": state, "HOME": filepath.Join(runtime, "home"), "TMPDIR": filepath.Join(runtime, "scratch")} {
			if !slices.Contains(env, name+"="+want) {
				t.Fatalf("%s for %s is not %s", name, key, want)
			}
			if prior, ok := seen[want]; ok {
				t.Fatalf("%s and %s share %s", prior, key, want)
			}
			seen[want] = key
		}
	}
}

func TestWorkspaceRejectsConflictsBeforeSideEffects(t *testing.T) {
	for _, name := range []string{"none", "workspace-root", "mcp", "relative", "missing", "ambient-setting", "duplicate-env", "bad-env"} {
		t.Run(name, func(t *testing.T) {
			config := workspaceFixture(t)
			req := workspaceRequest()
			switch name {
			case "none":
				req.DisableExecutionEnvironment = true
			case "workspace-root":
				req.LocalEnvironment = &proto.LocalEnvironment{ID: "environment", WorkspaceRoot: filepath.Join(config.RuntimeRoot, "scratch")}
			case "mcp":
				req.MCPHTTPServers = &[]proto.MCPHTTPServer{{ConnectionOrigin: "service", ServerLabel: "remote", ServerURL: "https://example.test/mcp"}}
			case "relative":
				config.Workspace = "relative"
			case "missing":
				config.Workspace = filepath.Join(config.Workspace, "missing")
			case "symlink":
				alias := filepath.Join(filepath.Dir(testState(config)), "alias")
				if err := os.Symlink(config.Workspace, alias); err != nil {
					t.Fatal(err)
				}
				config.Workspace = alias
			case "rule-pattern":
				config.Workspace += "*"
				if err := os.Mkdir(config.Workspace, 0o700); err != nil {
					t.Fatal(err)
				}
			case "ambient-setting":
				config.Env = append(config.Env, "NODE_OPTIONS=--require=untrusted")
			case "duplicate-env":
				config.Env = append(config.Env, "ANTHROPIC_AUTH_TOKEN=second")
			case "bad-env":
				config.Env = append(config.Env, "ANTHROPIC_API_KEY=bad\x00value")

			}
			if _, _, err := prepareConfiguration(config, req); err == nil {
				t.Fatal("invalid binding or request accepted")
			}
			if _, err := os.Stat(testState(config)); !os.IsNotExist(err) {
				t.Fatal("rejection created execution state")
			}
		})
	}
}

func TestWorkspaceRetainsDeclaredFunctions(t *testing.T) {
	config := workspaceFixture(t)
	req := workspaceRequest()
	req.FunctionTools = []proto.FunctionTool{{Name: "lookup", Parameters: json.RawMessage(`{"type":"object"}`)}}
	start, _, err := prepareConfiguration(config, req)
	if err != nil {
		t.Fatal(err)
	}
	if start.Workspace == nil || len(start.Functions) != 1 || start.Functions[0].Name != "lookup" || start.MCPHTTPServers != nil {
		t.Fatal("workspace function declaration was not retained independently of external MCP")
	}
}

func TestPublicMCPUsesWorkspaceProjectionWithoutCredentialCopy(t *testing.T) {
	config := workspaceFixture(t)
	req := workspaceRequest()
	req.LocalEnvironment = &proto.LocalEnvironment{WorkspaceRoot: config.Workspace}
	token := "vault-selected-canary"
	tools := []string{"prove"}
	req.MCPHTTPServers = &[]proto.MCPHTTPServer{{ConnectionOrigin: "environment", ServerLabel: "remote", ServerURL: "https://example.test/mcp", AllowedTools: &tools, Required: true, BearerToken: &token}}
	start, env, err := prepareConfiguration(config, req)
	if err != nil {
		t.Fatal(err)
	}
	if start.MCPHTTPServers != nil || start.Workspace == nil || len(start.Workspace.MCP) != 1 || !start.Workspace.MCP[0].Required || (*start.Workspace.MCP[0].AllowedTools)[0] != "prove" {
		t.Fatal("workspace policy lost")
	}
	raw, _ := json.Marshal(start)
	if strings.Contains(string(raw), token) {
		t.Fatal("bearer copied into bridge request")
	}
	ref := start.Workspace.MCP[0].BearerTokenEnvVar
	found := false
	for _, entry := range env {
		found = found || entry == ref+"="+token
	}
	if ref == "" || !found {
		t.Fatal("selected credential not bound")
	}
	info := RuntimeInfo{Protocol: 3, Features: []string{"workspace_tools", "workspace_prepare", "workspace_command_observations", "local_runtime_v2", "mcp_http_tools", "mcp_http_bearer_auth", "mcp_http_required"}}
	if validateExecutorFeatures(info, start) == nil {
		t.Fatal("unqualified workspace bridge admitted")
	}
	info.Features = append(info.Features, "workspace_mcp_http")
	if err := validateExecutorFeatures(info, start); err != nil {
		t.Fatal(err)
	}
}

const testStateKey = "session"

// testState is the native state directory the fixtures' Session writes to.
func testState(config Config) string {
	dir, err := agent.StateDir(config.StateRoot, harnessconfiguration.Declaration.Kind, testStateKey)
	if err != nil {
		panic(err)
	}
	return dir
}
