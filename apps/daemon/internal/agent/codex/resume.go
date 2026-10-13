package codex

import (
	"fmt"
	"strings"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

func (s *Session) resolveThread(req proto.PromptRequestPayload, plan SessionPlan) error {
	if strings.TrimSpace(req.AgentSessionID) != "" {
		if err := s.resumeThread(req.AgentSessionID, plan); err != nil {
			return fmt.Errorf("codex: thread/resume: %w", err)
		}
		return nil
	}
	if req.RequireExistingNativeSession {
		id, err := s.recoverRoot(plan)
		if err != nil {
			return err
		}
		if err := s.resumeThread(id, plan); err != nil {
			return fmt.Errorf("codex: recovered thread/resume: %w", err)
		}
		return nil
	}
	if err := s.startThread(plan); err != nil {
		return fmt.Errorf("codex: thread/start: %w", err)
	}
	return nil
}
