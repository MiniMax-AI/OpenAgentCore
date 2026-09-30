package mcode

import (
	"context"
	"fmt"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

func (s *Session) SubmitFunctionResult(context.Context, proto.FunctionResultPayload) error {
	return fmt.Errorf("%w: native public function tools are not qualified", agent.ErrUnsupportedOperation)
}

func (s *executor) ReadWorkspaceFile(context.Context, string, int) (agent.WorkspaceReadResult, error) {
	return agent.WorkspaceReadResult{}, agent.ErrWorkspaceReadUnsupported
}

func (s *executor) ListWorkspaceDirectory(context.Context, string, int) (agent.WorkspaceDirectoryResult, error) {
	return agent.WorkspaceDirectoryResult{}, agent.ErrWorkspaceReadUnsupported
}

func (s *executor) WriteWorkspaceFile(context.Context, string, []byte) (agent.WorkspaceWriteResult, error) {
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

func (s *prepared) ReadWorkspaceFile(context.Context, string, int) (agent.WorkspaceReadResult, error) {
	return agent.WorkspaceReadResult{}, agent.ErrWorkspaceReadUnsupported
}

func (s *prepared) ListWorkspaceDirectory(context.Context, string, int) (agent.WorkspaceDirectoryResult, error) {
	return agent.WorkspaceDirectoryResult{}, agent.ErrWorkspaceReadUnsupported
}

func (s *prepared) WriteWorkspaceFile(context.Context, string, []byte) (agent.WorkspaceWriteResult, error) {
	return agent.WorkspaceWriteResult{}, agent.ErrWorkspaceWriteUnsupported
}
