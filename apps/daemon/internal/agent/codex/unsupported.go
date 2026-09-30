package codex

import (
	"context"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
)

func (s *Executor) ReadWorkspaceFile(context.Context, string, int) (agent.WorkspaceReadResult, error) {
	return agent.WorkspaceReadResult{}, agent.ErrWorkspaceReadUnsupported
}

func (s *Executor) ListWorkspaceDirectory(context.Context, string, int) (agent.WorkspaceDirectoryResult, error) {
	return agent.WorkspaceDirectoryResult{}, agent.ErrWorkspaceReadUnsupported
}

func (s *Executor) WriteWorkspaceFile(context.Context, string, []byte) (agent.WorkspaceWriteResult, error) {
	return agent.WorkspaceWriteResult{}, agent.ErrWorkspaceWriteUnsupported
}

func (s *Session) ReadWorkspaceFile(context.Context, string, int) (agent.WorkspaceReadResult, error) {
	return agent.WorkspaceReadResult{}, agent.ErrWorkspaceReadUnsupported
}

func (s *Session) ListWorkspaceDirectory(context.Context, string, int) (agent.WorkspaceDirectoryResult, error) {
	return agent.WorkspaceDirectoryResult{}, agent.ErrWorkspaceReadUnsupported
}

func (s *Session) WriteWorkspaceFile(context.Context, string, []byte) (agent.WorkspaceWriteResult, error) {
	return agent.WorkspaceWriteResult{}, agent.ErrWorkspaceWriteUnsupported
}

func (s *Prepared) ReadWorkspaceFile(context.Context, string, int) (agent.WorkspaceReadResult, error) {
	return agent.WorkspaceReadResult{}, agent.ErrWorkspaceReadUnsupported
}

func (s *Prepared) ListWorkspaceDirectory(context.Context, string, int) (agent.WorkspaceDirectoryResult, error) {
	return agent.WorkspaceDirectoryResult{}, agent.ErrWorkspaceReadUnsupported
}

func (s *Prepared) WriteWorkspaceFile(context.Context, string, []byte) (agent.WorkspaceWriteResult, error) {
	return agent.WorkspaceWriteResult{}, agent.ErrWorkspaceWriteUnsupported
}
