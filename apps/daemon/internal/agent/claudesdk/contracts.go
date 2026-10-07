package claudesdk

import "github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"

var (
	_ agent.Executor = (*executor)(nil)
	_ agent.Turn     = (*session)(nil)
)
