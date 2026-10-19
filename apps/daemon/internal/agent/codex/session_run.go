package codex

import (
	"context"
	"fmt"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

func (s *Session) startNative(ctx context.Context, plan SessionPlan, req proto.PromptRequestPayload) error {
	if s.currentThreadID() == "" {
		if err := s.resolveThread(req, plan); err != nil {
			return err
		}
	}

	input, err := nativeInput(req.Input)
	if err != nil {
		return err
	}
	turnParams := TurnStartParams{
		ThreadID:     s.currentThreadID(),
		Input:        input,
		OutputSchema: plan.OutputSchema,
	}
	turnCtx, turnCancel := context.WithTimeout(ctx, 10*time.Second)
	_, ackErr := s.rpc.requestWithResult(turnCtx, "turn/start", turnParams, s.bindTurnResult)
	turnCancel()
	if ackErr != nil {
		s.cfg.logger.Warn("codex: turn/start ack failed", "run_id", s.runID, "err", ackErr)
		return fmt.Errorf("codex: turn/start: %w", ackErr)
	}

	return nil
}
