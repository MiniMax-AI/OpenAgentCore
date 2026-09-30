package codex

import (
	"context"
	"fmt"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent/installroot"
	"path/filepath"
	"runtime"
)

func Installation() agent.Installation {
	binary := func(dir string) string {
		name := "codex"
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		return filepath.Join(dir, "bin", name)
	}
	return agent.Installation{AgentKind: "codex", Version: "0.153.4", Supported: func() bool { return runtime.GOOS == "linux" || runtime.GOOS == "darwin" || runtime.GOOS == "windows" },
		Environment: func(dir, node string) map[string]string {
			return map[string]string{"OAC_RUNTIME_CODEX_BIN": binary(dir)}
		},
		Check: func(ctx context.Context, dir, node string, env []string) error {
			got, err := installroot.Probe(ctx, binary(dir), []string{"--version"}, env, dir)
			if err != nil || got != "codex-cli 0.153.4" {
				return fmt.Errorf("Codex installation is incompatible")
			}
			return nil
		}}
}
