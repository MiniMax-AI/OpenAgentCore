package mcode

import (
	"context"
	"fmt"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent/installroot"
	"path/filepath"
	"runtime"
)

func Installation() agent.Installation {
	return agent.Installation{AgentKind: "mcode", Version: SupportedVersion, Supported: func() bool { return runtime.GOOS == "linux" || runtime.GOOS == "darwin" },
		Environment: func(dir, node string) map[string]string {
			return map[string]string{"OAC_RUNTIME_MCODE_BIN": filepath.Join(dir, "native", "cli.js"), "OAC_RUNTIME_MCODE_NODE": node, "OAC_RUNTIME_MCODE_WORKSPACE_BRIDGE": filepath.Join(dir, "bridge.mjs"), "OAC_RUNTIME_MCODE_AGENTS_API": "1"}
		},
		Check: func(ctx context.Context, dir, node string, env []string) error {
			got, err := installroot.Probe(ctx, node, []string{filepath.Join(dir, "native", "cli.js"), "--version"}, env, dir)
			if err != nil || got != SupportedVersion {
				return fmt.Errorf("MiniMax installation is incompatible")
			}
			raw, err := installroot.Probe(ctx, node, []string{filepath.Join(dir, "check.mjs")}, env, dir)
			if err != nil || ValidateWorkspaceReadiness([]byte(raw)) != nil {
				return fmt.Errorf("MiniMax companion is unavailable or incompatible; use the current native distribution and verify Bash")
			}
			return nil
		}}
}
