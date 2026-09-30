package claudesdk

import (
	"context"
	"fmt"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

func (s *session) SubmitPermission(context.Context, string, proto.PermissionDecisionPayload) error {
	return fmt.Errorf("%w: native permission responses are not exposed", agent.ErrUnsupportedOperation)
}

func (s *session) SubmitPromptForUserChoice(context.Context, string, proto.PromptForUserChoiceDecisionPayload) error {
	return fmt.Errorf("%w: native user-choice responses are not exposed", agent.ErrUnsupportedOperation)
}

func (s *executor) WriteWorkspaceFile(context.Context, string, []byte) (agent.WorkspaceWriteResult, error) {
	return agent.WorkspaceWriteResult{}, agent.ErrWorkspaceWriteUnsupported
}

func (s *session) WriteWorkspaceFile(context.Context, string, []byte) (agent.WorkspaceWriteResult, error) {
	return agent.WorkspaceWriteResult{}, agent.ErrWorkspaceWriteUnsupported
}

func (s *prepared) WriteWorkspaceFile(context.Context, string, []byte) (agent.WorkspaceWriteResult, error) {
	return agent.WorkspaceWriteResult{}, agent.ErrWorkspaceWriteUnsupported
}
