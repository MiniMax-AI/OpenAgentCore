package codex

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

func TestReadOnlyPreparationRejectedBeforeNativeSetup(t *testing.T) {
	req, cfg, root := preparationFixture(t)
	req.WorkspaceReadOnly = true
	prepared, err := newPreparation(t.Context(), req, cfg)
	if err == nil || prepared != nil {
		t.Fatal("read-only request admitted", err)
	}
	if len(preparationFrames(t, root)) != 0 {
		t.Fatal("read-only request started native child")
	}
	if _, err := os.Stat(filepath.Join(root, "daemon", "agent-sessions")); !os.IsNotExist(err) {
		t.Fatal("read-only request created native state", err)
	}
}

func TestRetiredNativeTransportOptionsRejectedBeforeState(t *testing.T) {
	for _, key := range []string{"CODEX_EXEC_SERVER_URL", "CODEX_EXEC_SERVER_NOISE_REGISTRY_URL", "CODEX_EXEC_SERVER_NOISE_ENVIRONMENT_ID", "CODEX_EXEC_SERVER_NOISE_AUTH_TOKEN"} {
		for _, source := range []string{"process", "options"} {
			for _, none := range []bool{false, true} {
				t.Run(key+"/"+source+"/"+map[bool]string{false: "local", true: "none"}[none], func(t *testing.T) {
					req, cfg, root := preparationFixture(t)
					req.DisableExecutionEnvironment = none
					if !none {
						req.LocalEnvironment = &proto.LocalEnvironment{ID: "local"}
					}
					if source == "process" {
						t.Setenv(key, "retired-private-value")
					} else {
						req.AgentOptions["env"] = map[string]any{key: "retired-private-value"}
					}
					p, err := newPreparation(t.Context(), req, cfg)
					if p != nil || err == nil || !strings.Contains(err.Error(), "retired executor transport") || strings.Contains(err.Error(), "retired-private-value") {
						t.Fatal("transport override admitted or disclosed", err)
					}
					if len(preparationFrames(t, root)) != 0 {
						t.Fatal("retired transport started native process")
					}
					if _, err := os.Stat(filepath.Join(root, "daemon", "agent-sessions")); !os.IsNotExist(err) {
						t.Fatal("retired transport created state", err)
					}
				})
			}
		}
	}
}

func TestNativeNoneSelectorRemainsSupported(t *testing.T) {
	req, cfg, root := preparationFixture(t)
	t.Setenv("CODEX_EXEC_SERVER_URL", "none")
	req.AgentOptions["env"] = map[string]any{"CODEX_EXEC_SERVER_URL": "none"}
	p, err := newPreparation(t.Context(), req, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	assertPreparationOnly(t, root)
	statuses := 0
	for _, frame := range preparationFrames(t, root) {
		if frame.Method == "environment/status" {
			statuses++
		}
	}
	if statuses != 2 {
		t.Fatal("none did not verify both native execution environments")
	}
}
