package mcode

import (
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"path/filepath"
)

func Installation() agent.Installation {
	return agent.Installation{AgentKind: "mcode",
		Environment: func(dir, node string) map[string]string {
			return map[string]string{"OAC_RUNTIME_MCODE_BIN": filepath.Join(dir, "native", "cli.js"), "OAC_RUNTIME_MCODE_NODE": node, "OAC_RUNTIME_MCODE_WORKSPACE_BRIDGE": filepath.Join(dir, "bridge.mjs")}
		},
	}
}
