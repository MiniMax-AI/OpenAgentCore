package pi

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent/installroot"
)

// SupportedVersion is the Pi CLI this build is qualified against.
const SupportedVersion = "1.3.0"

// Installation describes how the installer exposes a bundled Pi to the Runtime.
// The component ships Pi's npm package and a `bin/pi` launcher that resolves
// `node` from the installer-provided PATH.
func Installation() agent.Installation {
	return agent.Installation{
		AgentKind: "pi",
		Version:   SupportedVersion,
		Supported: func() bool { return runtime.GOOS == "linux" || runtime.GOOS == "darwin" },
		Environment: func(dir, _ string) map[string]string {
			return map[string]string{"OAC_RUNTIME_PI_BIN": filepath.Join(dir, "bin", "pi")}
		},
		Check: func(ctx context.Context, dir, _ string, env []string) error {
			got, err := installroot.Probe(ctx, filepath.Join(dir, "bin", "pi"), []string{"--version"}, env, dir)
			if err != nil || got != SupportedVersion {
				return fmt.Errorf("Pi installation is incompatible")
			}
			return nil
		},
	}
}
