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
