package claudesdk

import (
	"context"
	"fmt"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	declaration "github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig/claudesdk"
	"path/filepath"
	"runtime"
)

func Installation() agent.Installation {
	return agent.Installation{AgentKind: declaration.Declaration.Kind, Version: declaration.Declaration.Native.Version, Supported: func() bool { return runtime.GOOS == "linux" || runtime.GOOS == "darwin" || runtime.GOOS == "windows" },
		Environment: func(dir, node string) map[string]string {
			return map[string]string{claudeSDKEntrypointEnv: filepath.Join(dir, "dist", "main.js"), claudeSDKNodeEnv: node}
		},
		Check: func(ctx context.Context, dir, node string, env []string) error {
			got, err := CheckRuntime(ctx, Config{Node: node, Entrypoint: filepath.Join(dir, "dist", "main.js"), Env: env})
			if err != nil || got.SDK != declaration.Declaration.Native.Version || !got.SupportsLocalRuntime() {
				return fmt.Errorf("Claude installation is incompatible; Windows requires Git Bash")
			}
			return nil
		}}
}
