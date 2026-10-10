package codex

import (
	"os"
	"testing"
)

func testStateRoot(t *testing.T) string {
	t.Helper()
	if root := os.Getenv("OAC_RUNTIME_HOME"); root != "" {
		return root
	}
	root := t.TempDir()
	t.Setenv("OAC_RUNTIME_HOME", root)
	return root
}
