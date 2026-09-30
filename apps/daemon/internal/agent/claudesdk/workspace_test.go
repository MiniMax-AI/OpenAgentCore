package claudesdk

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

func workspaceFixture(t *testing.T) Config {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("OAC_RUNTIME_HOME", root)
	for _, name := range []string{"workspace", "home", "state", "scratch", "secrets", "bin", "runtime/dist", "runtime/node_modules"} {
		if err := os.MkdirAll(filepath.Join(root, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	config := Config{Node: filepath.Join(root, "bin", "node"), Entrypoint: filepath.Join(root, "runtime", "dist", "main.js"), StateDir: filepath.Join(root, "state"),
		Env: []string{"ANTHROPIC_AUTH_TOKEN=selected-provider-fixture", "ANTHROPIC_BASE_URL=https://example.invalid"},
		Workspace: &WorkspaceConfig{Directory: filepath.Join(root, "workspace"), HomeDir: filepath.Join(root, "home"),
			ScratchDir: filepath.Join(root, "scratch")}}
	for _, name := range []string{config.Node, config.Entrypoint, filepath.Join(filepath.Dir(config.Entrypoint), "runtime_check.js")} {
		if err := os.WriteFile(name, nil, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return config
}

func workspaceRequest() proto.PromptRequestPayload {
	return proto.PromptRequestPayload{RunID: "run", Input: proto.TextInput("hello"), DisableSubagents: true,
		AgentOptions: map[string]any{"model": "fixture"}}
}

func TestWorkspaceTrustedBindingAndEnvironment(t *testing.T) {
	config := workspaceFixture(t)
	t.Setenv("OAC_TEST_PARENT_SECRET", "parent-only")
	t.Setenv("ANTHROPIC_API_KEY", "unselected-provider")
	start, env, err := prepare(config, workspaceRequest())
	if err != nil {
		t.Fatal(err)
	}
	if start.Cwd != config.Workspace.Directory || start.Workspace == nil || start.MCPHTTPServers != nil {
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
	if values["HOME"] != config.Workspace.HomeDir || values["CLAUDE_CONFIG_DIR"] != config.StateDir ||
		values["TMPDIR"] != config.Workspace.ScratchDir || values["ANTHROPIC_AUTH_TOKEN"] != "selected-provider-fixture" {
		t.Fatal("explicit runtime environment was not preserved")
	}
	entries, err := os.ReadDir(config.StateDir)
	if err != nil || len(entries) != 0 {
		t.Fatal("preparation created history or nested scratch")
	}
}

func TestWorkspaceRejectsConflictsBeforeSideEffects(t *testing.T) {
	for _, name := range []string{"none", "workspace-root", "mcp", "caller-policy", "relative", "missing", "ambient-setting", "duplicate-env", "bad-env"} {
		t.Run(name, func(t *testing.T) {
			config := workspaceFixture(t)
			req := workspaceRequest()
			switch name {
			case "none":
				req.DisableExecutionEnvironment = true
			case "workspace-root":
				config.Workspace.NetworkAccess = "enabled"
				req.LocalEnvironment = &proto.LocalEnvironment{ID: "environment", NetworkAccess: "enabled", WorkspaceRoot: config.Workspace.ScratchDir}
			case "mcp":
				req.MCPHTTPServers = &[]proto.MCPHTTPServer{{ConnectionOrigin: "service", ServerLabel: "remote", ServerURL: "https://example.test/mcp"}}
			case "caller-policy":
				req.AgentOptions["workspace"] = "override"
			case "relative":
				config.Workspace.Directory = "relative"
			case "missing":
				config.Workspace.Directory = filepath.Join(config.Workspace.Directory, "missing")
			case "overlap":
				config.Workspace.ScratchDir = config.StateDir
			case "symlink":
				alias := filepath.Join(filepath.Dir(config.StateDir), "alias")
				if err := os.Symlink(config.Workspace.Directory, alias); err != nil {
					t.Fatal(err)
				}
				config.Workspace.Directory = alias
			case "rule-pattern":
				config.Workspace.Directory += "*"
				if err := os.Mkdir(config.Workspace.Directory, 0o700); err != nil {
					t.Fatal(err)
				}
			case "ambient-setting":
				config.Env = append(config.Env, "NODE_OPTIONS=--require=untrusted")
			case "duplicate-env":
				config.Env = append(config.Env, "ANTHROPIC_AUTH_TOKEN=second")
			case "bad-env":
				config.Env = append(config.Env, "ANTHROPIC_API_KEY=bad\x00value")

			}
			if _, _, err := prepare(config, req); err == nil {
				t.Fatal("invalid binding or request accepted")
			}
			entries, err := os.ReadDir(config.StateDir)
			if err != nil || len(entries) != 0 {
				t.Fatal("rejection created execution state")
			}
		})
	}
}

func TestWorkspaceRetainsDeclaredFunctions(t *testing.T) {
	config := workspaceFixture(t)
	req := workspaceRequest()
	req.FunctionTools = []proto.FunctionTool{{Name: "lookup", Parameters: json.RawMessage(`{"type":"object"}`)}}
	start, _, err := prepare(config, req)
	if err != nil {
		t.Fatal(err)
	}
	if start.Workspace == nil || len(start.Functions) != 1 || start.Functions[0].Name != "lookup" || start.MCPHTTPServers != nil {
		t.Fatal("workspace function declaration was not retained independently of external MCP")
	}
}

func TestPublicMCPUsesWorkspaceProjectionWithoutCredentialCopy(t *testing.T) {
	config := workspaceFixture(t)
	config.Workspace.NetworkAccess = "enabled"
	req := workspaceRequest()
	req.LocalEnvironment = &proto.LocalEnvironment{NetworkAccess: "enabled", WorkspaceRoot: config.Workspace.Directory}
	token := "vault-selected-canary"
	tools := []string{"prove"}
	req.MCPHTTPServers = &[]proto.MCPHTTPServer{{ConnectionOrigin: "environment", ServerLabel: "remote", ServerURL: "https://example.test/mcp", AllowedTools: &tools, Required: true, BearerToken: &token}}
	start, env, err := prepare(config, req)
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
