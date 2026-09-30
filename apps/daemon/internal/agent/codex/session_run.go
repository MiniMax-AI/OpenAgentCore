package codex

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

func (s *Session) run(plan SessionPlan, req proto.PromptRequestPayload) {
	defer close(s.waitDone)
	defer s.stopCodexInteractionTimers()
	defer s.stopFunctionCalls()
	defer s.cleanup()
	defer s.closeRunOutput()

	if err := s.startNative(s.cancelCtx, plan, req); err != nil {
		s.emitTerminal(err.Error(), true)
		return
	}

	// Block until terminal handlers close the RPC child or cancellation arrives.
	select {
	case <-s.rpc.Done():
		if !s.cancelled.Load() && s.cancelCtx.Err() == nil {
			s.emitTerminal("codex: connection closed before the run completed", true)
		}
	case <-s.cancelCtx.Done():
		_ = s.rpc.Close()
	}
}

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
		ThreadID: s.currentThreadID(),
		Input:    input,
	}
	if plan.CollaborationMode != "" {
		model := strings.TrimSpace(s.resolvedModel)
		if model == "" {
			return fmt.Errorf("codex: collaboration mode requires a resolved model")
		}
		var developerInstructions *string
		if plan.SystemPrompt != "" {
			developerInstructions = &plan.SystemPrompt
		}
		turnParams.CollaborationMode = &CollaborationMode{
			Mode: plan.CollaborationMode,
			Settings: CollaborationModeSettings{
				ReasoningEffort:       plan.ModelReasoningEffort,
				Model:                 model,
				DeveloperInstructions: developerInstructions,
			},
		}
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
