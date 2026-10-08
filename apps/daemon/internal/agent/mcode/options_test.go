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
	install, session := fakeInstall("node"), hostSession(t)
	req := testRequest(t)
	opts, err := install.prepare(prepared(t, req), session)
	if err != nil {
		t.Fatal(err)
	}
	var dataDirs []string
	for _, entry := range opts.Env {
		if value, ok := strings.CutPrefix(entry, "MINIMAX_DATA_DIR="); ok {
			dataDirs = append(dataDirs, value)
		}
	}
	if len(dataDirs) != 1 || dataDirs[0] != opts.DataDir {
		t.Fatal("native data directory is not the adapter's")
	}
	req.SystemPrompt = ""
	req.AgentSessionID = "native-1"
	refreshed, err := install.prepare(prepared(t, req), session)
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
			if _, err := fakeInstall("node").prepare(prepared(t, req), hostSession(t)); err == nil {
				t.Fatal("expected validation failure")
			}
		})
	}
}
