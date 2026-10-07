package claudesdk

import (
	"context"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
)

func (s *executor) WriteWorkspaceFile(context.Context, string, []byte) (agent.WorkspaceWriteResult, error) {
	return agent.WorkspaceWriteResult{}, agent.ErrWorkspaceWriteUnsupported
}

func (s *session) WriteWorkspaceFile(context.Context, string, []byte) (agent.WorkspaceWriteResult, error) {
	return agent.WorkspaceWriteResult{}, agent.ErrWorkspaceWriteUnsupported
}
