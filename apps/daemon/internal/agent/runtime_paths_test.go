package agent

import (
	"os"
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

func TestRemoveSessionStateKeepsOnlyTheNamedKey(t *testing.T) {
	stateRoot, runtimeRoot := t.TempDir(), t.TempDir()
	var kept, removed []string
	for _, root := range []string{stateRoot, runtimeRoot} {
		for _, kind := range []string{"codex", "claude-sdk"} {
			keep, err := StateDir(root, kind, "keep")
			if err != nil {
				t.Fatal(err)
			}
			other, _ := StateDir(root, kind, "other")
			for _, dir := range []string{keep, other} {
				if err := os.MkdirAll(filepath.Join(dir, "history"), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			kept, removed = append(kept, keep), append(removed, other)
		}
	}
	if err := os.WriteFile(filepath.Join(stateRoot, "runtime", ".DS_Store"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RemoveSessionState(stateRoot, runtimeRoot, "keep"); err != nil {
		t.Fatal(err)
	}
	for _, dir := range kept {
		if _, err := os.Stat(dir); err != nil {
			t.Fatalf("kept state removed: %v", err)
		}
	}
	for _, dir := range removed {
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Fatalf("other state kept: %s", dir)
		}
	}
	if err := RemoveSessionState(t.TempDir(), t.TempDir(), "keep"); err != nil {
		t.Fatalf("empty roots: %v", err)
	}
}
