package agent

import (
	"path/filepath"
	"testing"
)

func TestStateDirUsesStableAgentState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OAC_RUNTIME_HOME", home)
	got, err := StateDir("codex", "conv-1/agent-1/codex", "ignored", "ignored")
	if err != nil {
		t.Fatalf("StateDir: %v", err)
	}
	want := filepath.Join(home, "runtime", "codex", "state", "conv-1", "agent-1", "codex")
	if got != want {
		t.Fatalf("root = %q, want %q", got, want)
	}
}

func TestStateDirSanitizesFallback(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OAC_RUNTIME_HOME", home)
	got, err := StateDir("fake_beta", "", "../conv name", "ignored")
	if err != nil {
		t.Fatalf("StateDir: %v", err)
	}
	want := filepath.Join(home, "runtime", "fake_beta", "conv-.._conv_name")
	if got != want {
		t.Fatalf("root = %q, want %q", got, want)
	}
}
