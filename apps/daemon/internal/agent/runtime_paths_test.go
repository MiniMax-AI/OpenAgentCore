package agent

import (
	"path/filepath"
	"testing"
)

func TestStateDirUsesStableAgentState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OAC_RUNTIME_HOME", home)
	got, err := StateDir(home, "codex", "conv-1/agent-1/codex")
	if err != nil {
		t.Fatalf("StateDir: %v", err)
	}
	want := filepath.Join(home, "runtime", "codex", "state", "conv-1", "agent-1", "codex")
	if got != want {
		t.Fatalf("root = %q, want %q", got, want)
	}
}

func TestStateDirRequiresAgentState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OAC_RUNTIME_HOME", home)
	for _, key := range []string{"", " ", "../.."} {
		if _, err := StateDir(home, "codex", key); err == nil {
			t.Fatalf("state key %q accepted", key)
		}
	}
	got, err := StateDir(home, "codex", "../session-1/a b")
	if want := filepath.Join(home, "runtime", "codex", "state", "session-1", "a_b"); err != nil || got != want {
		t.Fatalf("sanitized state = %q, %v; want %q", got, err, want)
	}
}
