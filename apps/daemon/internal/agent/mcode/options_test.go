package mcode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

func TestOptionsRefreshManagedState(t *testing.T) {
	t.Setenv("MINIMAX_DATA_DIR", "/wrong")
	req := testRequest(t)
	opts, err := prepareOptions(prepared(t, req))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(opts.Dir, os.Getenv("OAC_RUNTIME_HOME")+string(os.PathSeparator)) {
		t.Fatalf("workdir escaped managed state: %s", opts.Dir)
	}
	dataDir := ""
	for _, entry := range opts.Env {
		if value, ok := strings.CutPrefix(entry, "MINIMAX_DATA_DIR="); ok {
			dataDir = value
		}
	}
	if dataDir != opts.DataDir {
		t.Fatal("native data directory is not the adapter's")
	}
	req.SystemPrompt = ""
	req.AgentSessionID = "native-1"
	refreshed, err := prepareOptions(prepared(t, req))
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.DataDir != opts.DataDir {
		t.Fatal("resume moved native state")
	}
	content, err := os.ReadFile(filepath.Join(opts.DataDir, "AGENTS.md"))
	if err != nil || len(content) != 0 {
		t.Fatal("removed instructions retained on resume")
	}
	data, err := os.ReadFile(filepath.Join(opts.DataDir, "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if json.Unmarshal(data, &cfg) != nil {
		t.Fatal("invalid config")
	}
	if cfg["permissionMode"] != "auto" {
		t.Fatal("native permission mode is not auto")
	}
	if cfg["skills"].(map[string]any)["external"].(map[string]any)["enabled"] != false {
		t.Fatal("external discovery enabled")
	}
	info, _ := os.Stat(filepath.Join(opts.DataDir, "config.yaml"))
	if info.Mode().Perm() != 0600 {
		t.Fatal("native credentials file is not private")
	}
}

func TestOptionsRejectDroppedContext(t *testing.T) {
	tests := []struct {
		name string
		edit func(*proto.PromptRequestPayload)
	}{
		{"oversized instructions", func(r *proto.PromptRequestPayload) { r.SystemPrompt = strings.Repeat("x", 32*1024+1) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := testRequest(t)
			tt.edit(&req)
			if _, err := prepareOptions(prepared(t, req)); err == nil {
				t.Fatal("expected validation failure")
			}
		})
	}
}

func TestDataDirectoryRequiresAgentState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OAC_RUNTIME_HOME", home)
	for _, key := range []string{"", " ", "../.."} {
		if _, err := dataDirectory(key); err == nil {
			t.Fatalf("state key %q accepted", key)
		}
	}
	got, err := dataDirectory("../session-1/a b")
	if want := filepath.Join(home, "runtime", "mcode", "state", "session-1", "a_b"); err != nil || got != want {
		t.Fatalf("data directory = %q, %v; want %q", got, err, want)
	}
}
