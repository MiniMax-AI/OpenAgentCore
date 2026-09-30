package agent

import (
	"path/filepath"
	"testing"
)

func TestManagedSkillsRootUsesStableAgentState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OAC_RUNTIME_HOME", home)
	got, err := ManagedSkillsRoot("codex", "conv-1/agent-1/codex", "ignored", "ignored")
	if err != nil {
		t.Fatalf("ManagedSkillsRoot: %v", err)
	}
	want := filepath.Join(home, "runtime", "codex", "state", "conv-1", "agent-1", "codex", "skills")
	if got != want {
		t.Fatalf("root = %q, want %q", got, want)
	}
}

func TestManagedSkillsRootSanitizesFallback(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OAC_RUNTIME_HOME", home)
	got, err := ManagedSkillsRoot("fake_beta", "", "../conv name", "ignored")
	if err != nil {
		t.Fatalf("ManagedSkillsRoot: %v", err)
	}
	want := filepath.Join(home, "runtime", "fake_beta", "conv-.._conv_name", "skills")
	if got != want {
		t.Fatalf("root = %q, want %q", got, want)
	}
}
