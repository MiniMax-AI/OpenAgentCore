package codex

import (
	"context"
	"fmt"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent/installroot"
	declaration "github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig/codex"
	"path/filepath"
	"runtime"
)

// versionOutput is what the pinned native binary prints for --version.
var versionOutput = "codex-cli " + declaration.Declaration.Native.Version

func Installation() agent.Installation {
	binary := func(dir string) string {
		name := "codex"
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		return filepath.Join(dir, "bin", name)
	}
	return agent.Installation{AgentKind: declaration.Declaration.Kind, Version: declaration.Declaration.Native.Version, Supported: func() bool { return runtime.GOOS == "linux" || runtime.GOOS == "darwin" || runtime.GOOS == "windows" },
		Environment: func(dir, node string) map[string]string {
			return map[string]string{binaryEnv: binary(dir)}
		},
		Check: func(ctx context.Context, dir, node string, env []string) error {
			got, err := installroot.Probe(ctx, binary(dir), []string{"--version"}, env, dir)
			if err != nil || got != versionOutput {
				return fmt.Errorf("Codex installation is incompatible")
			}
			return nil
		}}
}
