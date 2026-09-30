package claudesdk

import (
	"context"
	"fmt"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"path/filepath"
	"runtime"
)

func Installation() agent.Installation {
	return agent.Installation{AgentKind: "claude_sdk", Version: "0.3.269", Supported: func() bool { return runtime.GOOS == "linux" || runtime.GOOS == "darwin" || runtime.GOOS == "windows" },
		Environment: func(dir, node string) map[string]string {
			return map[string]string{"OAC_RUNTIME_CLAUDE_SDK_ENTRYPOINT": filepath.Join(dir, "dist", "main.js"), "OAC_RUNTIME_CLAUDE_SDK_NODE": node}
		},
		Check: func(ctx context.Context, dir, node string, env []string) error {
			got, err := CheckRuntime(ctx, Config{Node: node, Entrypoint: filepath.Join(dir, "dist", "main.js"), Env: env})
			if err != nil || got.SDK != "0.3.269" || !got.SupportsLocalRuntime() {
				return fmt.Errorf("Claude installation is incompatible; Windows requires Git Bash")
			}
			return nil
		}}
}
