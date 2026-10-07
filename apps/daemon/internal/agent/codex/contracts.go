package codex

import "github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"

var (
	_ agent.Executor = (*Executor)(nil)
	_ agent.Turn     = (*Session)(nil)
)
