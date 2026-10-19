package mcode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

func readNativeCapabilities(t *testing.T, dataDir string) (map[string]any, map[string]any) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dataDir, "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := json.Unmarshal(raw, &config); err != nil {
		t.Fatal(err)
	}
	return config, config["agents"].(map[string]any)["default"].(map[string]any)
}

func TestWorkspaceRunsNativeToolsWithoutExtraServers(t *testing.T) {
	native := []any{"read", "write", "edit", "bash", "grep", "glob", "todowrite"}
	for _, test := range []struct {
		name                string
		subagents, disabled bool
		tools               []any
	}{
		{"single agent", false, false, append(slices.Clone(native), "web_fetch")},
		{"subagents", true, false, append(slices.Clone(native), "web_fetch")},
		{"search disabled", true, true, native},
	} {
		t.Run(test.name, func(t *testing.T) {
			c, req, _ := workspaceFixture(t)
			if test.subagents {
				dir := t.TempDir()
				for _, name := range []string{"node", "subagent-snapshot.mjs"} {
					if err := os.WriteFile(filepath.Join(dir, name), nil, 0700); err != nil {
						t.Fatal(err)
					}
				}
				t.Setenv("OAC_RUNTIME_MCODE_NODE", filepath.Join(dir, "node"))
				t.Setenv("OAC_RUNTIME_MCODE_COMPANION", dir)
				limit := 2
				req.DisableSubagents, req.MaxConcurrentSubagents = false, &limit
			}
			if test.disabled {
				req.ExecutionControls = &proto.ExecutionControls{WebSearch: &proto.WebSearchControl{Mode: "disabled", ContextSize: "medium"}}
			}
			opts, err := prepareWorkspaceOptions(testStateRoot(t), c, req)
			if err != nil {
				t.Fatal(err)
			}
			config, capabilities := readNativeCapabilities(t, opts.DataDir)
			features := capabilities["features"].(map[string]any)
			if _, ok := capabilities["skills"]; ok || !reflect.DeepEqual(capabilities["tools"], test.tools) || features["delegation"] != test.subagents ||
				config["permissionMode"] != "bypassPermissions" || len(opts.MCP) != 0 || opts.Dir != c.Directory {
				t.Fatalf("unexpected native configuration: %v %v", config, opts.MCP)
			}
			if !slices.Contains(opts.Env, "OAC_RUNTIME_MCODE_BACKGROUND_BASH=off") {
				t.Fatal("background bash is not disabled")
			}
			if _, err := os.Stat(filepath.Join(opts.DataDir, "mcp.json")); !os.IsNotExist(err) {
				t.Fatalf("adapter wrote a native MCP file: %v", err)
			}
		})
	}
}
