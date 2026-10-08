package claudesdk

import (
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"path/filepath"
)

func Installation() agent.Installation {
	return agent.Installation{AgentKind: "claude_sdk",
		Environment: func(dir, node string) map[string]string {
			return map[string]string{"OAC_RUNTIME_CLAUDE_SDK_ENTRYPOINT": filepath.Join(dir, "dist", "main.js"), "OAC_RUNTIME_CLAUDE_SDK_NODE": node}
		},
	}
}
