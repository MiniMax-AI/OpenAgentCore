package mcode

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestWorkspaceReadinessRejectsOldCleanupContract(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("MiniMax adapter supports Linux and macOS")
	}
	for _, protocol := range []int{1, 2} {
		t.Run(fmt.Sprint(protocol), func(t *testing.T) {
			dir := t.TempDir()
			node := filepath.Join(dir, "node")
			body := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' '{\"protocol\":%d,\"native\":\"0.4.12\",\"source\":\"33b259bbbeb1c16433390869938191d09bdb0680\"}'\n", protocol)
			if err := os.WriteFile(node, []byte(body), 0700); err != nil {
				t.Fatal(err)
			}
			err := CheckWorkspace(context.Background(), WorkspaceConfig{Node: node, Bridge: filepath.Join(dir, "bridge.mjs")})
			if (err == nil) != (protocol == 2) {
				t.Fatalf("protocol %d readiness: %v", protocol, err)
			}
		})
	}
}

func TestValidateWorkspaceReadinessRejectsInvalidDescriptors(t *testing.T) {
	for _, raw := range []string{"", "null", "{", strings.Repeat(" ", 4097),
		`{"protocol":1,"native":"0.4.12","source":"33b259bbbeb1c16433390869938191d09bdb0680"}`,
		`{"protocol":2,"native":"0.4.11","source":"33b259bbbeb1c16433390869938191d09bdb0680"}`,
		`{"protocol":2,"native":"0.4.12","source":"other"}`} {
		if err := ValidateWorkspaceReadiness([]byte(raw)); err == nil || err.Error() != "mcode: workspace companion check failed" {
			t.Fatal("invalid descriptor accepted or exposed", err)
		}
	}
}
