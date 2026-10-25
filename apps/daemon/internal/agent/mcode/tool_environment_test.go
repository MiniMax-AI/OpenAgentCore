package mcode

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/localworkspace"
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
	capabilityDirectory := t.TempDir()
	req.AgentStateKey = session
	req.LocalEnvironment.ID, req.LocalEnvironment.WorkspaceDirectory = environment, config.Directory
	req.LocalEnvironment.Capabilities = true
	req.LocalEnvironment.CapabilitySources = &proto.CapabilitySources{Directories: []string{plugin}}
	for _, resume := range []bool{false, true} {
		if resume {
			req.AgentSessionID = "native-1"
			write(source, `{"MCP_TEST_TOKEN":"changed","TOOL_SECRET":"changed"}`)
		}
		binding, err := localworkspace.NewWithCapabilityDirectory(environment, session, config.Directory, capabilityDirectory)
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
		opts, err := prepareWorkspaceOptions(testStateRoot(t), config, prepared)
		if err != nil {
			t.Fatal(err)
		}
		// The native process, and so its tools, sees the Runtime snapshot, not a later change to the source.
		for _, want := range []string{"MCP_TEST_TOKEN=fixture-bearer-canary", "TOOL_SECRET=fixture-tool-canary"} {
			if !slices.Contains(opts.Env, want) {
				t.Fatal("native environment lacks the frozen tool environment", want)
			}
		}
		// Assert the actual env-selected HTTP credential, not a manually injected token.
		servers, err := runtimeMCP(prepared)
		if err != nil {
			t.Fatal(err)
		}
		headers, ok := servers[0]["headers"].([]map[string]string)
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
	binding, err := localworkspace.NewWithCapabilityDirectory(environment, session, config.Directory, capabilityDirectory)
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
	if _, err := prepareWorkspaceOptions(testStateRoot(t), config, req); err == nil {
		t.Fatal("invalid explicit tool configuration was ignored")
	}
}
