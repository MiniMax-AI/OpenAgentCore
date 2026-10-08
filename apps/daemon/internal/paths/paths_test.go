package paths_test

import (
	"path/filepath"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/paths"
)

func TestRootAndFilesHonourRuntimeHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OAC_RUNTIME_HOME", home)
	if got, err := paths.Root(); err != nil || got != home {
		t.Fatalf("Root = %q, %v, want %q", got, err, home)
	}
	for name, file := range map[string]func() (string, error){"connect.pid": paths.PIDFile, "connect.log": paths.LogFile} {
		if got, err := file(); err != nil || got != filepath.Join(home, "daemon", "default", name) {
			t.Errorf("%s = %q, %v", name, got, err)
		}
	}
}

func TestRootRejectsRelativeOverride(t *testing.T) {
	t.Setenv("OAC_RUNTIME_HOME", "relative")
	if _, err := paths.Root(); err == nil {
		t.Fatal("relative private home accepted")
	}
}
