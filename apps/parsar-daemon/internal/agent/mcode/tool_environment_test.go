package mcode

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/localworkspace"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentcapabilities"
	"github.com/google/uuid"
)

func TestWorkspaceCredentialsRemainInRuntimeSnapshotAcrossReconnect(t *testing.T) {
	config, req, _ := workspaceFixture(t)
	source := filepath.Join(t.TempDir(), "operator.json")
	write := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(source, `{"MCP_TEST_TOKEN":"fixture-bearer-canary","TOOL_SECRET":"fixture-tool-canary"}`)
	initialization := t.TempDir()
	t.Setenv("OAC_RUNTIME_INITIALIZATION_DIRECTORY", initialization)
	t.Setenv("OAC_RUNTIME_TOOL_ENV_FILE", source)
	plugin := t.TempDir()
	write(filepath.Join(plugin, ".codex-plugin", "plugin.json"), `{"name":"remote","description":"Credential fixture","mcpServers":"./.mcp.json"}`)
	write(filepath.Join(plugin, ".mcp.json"), `{"mcpServers":{"remote":{"type":"http","url":"https://example.invalid/mcp","bearer_token_env_var":"MCP_TEST_TOKEN"}}}`)
	environment, session := uuid.NewString(), uuid.NewString()
	t.Setenv("OAC_RUNTIME_ENVIRONMENT_ID", environment)
	t.Setenv("OAC_RUNTIME_SESSION_ID", session)
	t.Setenv("OAC_RUNTIME_WORKSPACE", config.Directory)
	t.Setenv("OAC_RUNTIME_CAPABILITY_DIRECTORY", t.TempDir())
	t.Setenv("OAC_RUNTIME_NETWORK_ACCESS", "enabled")
	t.Setenv("OAC_RUNTIME_ALLOWED_DOMAINS", "")
	req.WorkDir, req.AgentStateKey = "", "agents-api-"+session
	req.LocalEnvironment.ID, req.LocalEnvironment.WorkspaceDirectory = environment, config.Directory
	req.LocalEnvironment.Capabilities = true
	req.LocalEnvironment.CapabilitySources = &agentcapabilities.Input{Directories: []string{plugin}}
	for _, resume := range []bool{false, true} {
		if resume {
			req.AgentSessionID = "native-1"
			write(source, `{"MCP_TEST_TOKEN":"changed","TOOL_SECRET":"changed"}`)
		}
		binding, err := localworkspace.Load()
		if err != nil {
			t.Fatal(err)
		}
		configured, err := binding.Configure(req)
		if err != nil {
			t.Fatal(err)
		}
		prepared, err := binding.Prepare(t.Context(), configured)
		if err != nil {
			t.Fatal(err)
		}
		opts, err := prepareWorkspaceOptions(t.Context(), config, prepared)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(filepath.Join(opts.DataDir, "workspace-profile.json"))
		if err != nil {
			t.Fatal(err)
		}
		var profile struct {
			ToolEnvFile string `json:"toolEnvFile"`
		}
		if err := json.Unmarshal(raw, &profile); err != nil {
			t.Fatal(err)
		}
		if profile.ToolEnvFile != filepath.Join(initialization, "tool-env.json") {
			t.Fatal("native profile did not retain the Runtime snapshot reference")
		}
		// Assert the actual env-selected HTTP credential, not a manually injected token.
		headers, ok := opts.MCP[1]["headers"].([]map[string]string)
		if !ok || len(headers) != 1 || headers[0]["name"] != "Authorization" || headers[0]["value"] != "Bearer fixture-bearer-canary" {
			t.Fatal("frozen MCP credential was lost or replaced")
		}
		if err := filepath.WalkDir(opts.DataDir, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				return nil
			}
			body, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if bytes.Contains(body, []byte("fixture-bearer-canary")) || bytes.Contains(body, []byte("fixture-tool-canary")) {
				t.Fatal("tool credential copied into native persisted state")
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Remove(filepath.Join(initialization, "tool-env.json")); err != nil {
		t.Fatal(err)
	}
	binding, err := localworkspace.Load()
	if err != nil {
		t.Fatal(err)
	}
	configured, err := binding.Configure(req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := binding.Prepare(t.Context(), configured); err == nil {
		t.Fatal("missing frozen credential source was recreated or fell back to anonymous")
	}
}

func TestWorkspaceRejectsInvalidToolEnvironment(t *testing.T) {
	config, req, _ := workspaceFixture(t)
	file := filepath.Join(t.TempDir(), "tool-env.json")
	if err := os.WriteFile(file, []byte(`[]`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OAC_RUNTIME_TOOL_ENV_FILE", file)
	t.Setenv("OAC_RUNTIME_INITIALIZATION_DIRECTORY", t.TempDir())
	if _, err := prepareWorkspaceOptions(t.Context(), config, req); err == nil {
		t.Fatal("invalid explicit tool configuration was ignored")
	}
}
