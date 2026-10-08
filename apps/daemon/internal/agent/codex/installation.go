package codex

import (
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
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
	return agent.Installation{AgentKind: "codex",
		Environment: func(dir, node string) map[string]string {
			return map[string]string{"OAC_RUNTIME_CODEX_BIN": binary(dir)}
		},
	}
}
