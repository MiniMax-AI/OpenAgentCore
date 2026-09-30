package mcode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

func (s *Session) runExecutorTurn() {
	err := s.captureSubagentBaseline()
	if err == nil {
		err = s.executePrompt()
	}
	if errors.Is(err, errTurnCancelled) {
		err = nil
	}
	s.mu.Lock()
	s.closing, s.steeringReady = true, false
	close(s.inputDone)
	s.mu.Unlock()
	// Written steering requests retain their original receipt owner even after
	// prompt completion. No successor starts until all callers have settled.
	s.operations.Wait()
	if s.req.StrictResume && !s.req.DisableSubagents && s.subagentHistoryReady {
		childErr := s.settleSubagents()
		s.mu.Lock()
		s.subagentSettlementError = childErr
		s.mu.Unlock()
		if childErr != nil {
			err = childErr
		}
	}
	reusable := err == nil && s.process.Context().Err() == nil
	if len(s.tools) > 0 {
		reusable = false
	}
	s.finishEnvironmentMCP()
	s.mu.Lock()
	// ACP and native history cancellation do not prove detached tool cleanup.
	// Retire the owner and settle its workers before acknowledging cancellation.
	if s.cancelled || s.inputUncertain || len(s.permissions) != 0 || len(s.questions) != 0 {
		reusable = false
	}
	if s.inputUncertain && err == nil {
		err = fmt.Errorf("mcode: native input outcome is unknown")
	}
	metadata := map[string]any{proto.DoneMetaAgentSessionType: "mcode", proto.DoneMetaAgentSessionID: s.sessionID}
	s.outcome = proto.DonePayload{Content: s.content.String(), Metadata: metadata, SourceCompletedAtMS: s.rootCompletedAtMS}
	outcome := s.outcome
	s.permissions, s.questions = map[string]pendingPermission{}, map[string]pendingQuestion{}
	s.mu.Unlock()
	if !reusable {
		// Unknown quiescence invalidates this owner. A successful settlement
		// still proves its native process group and pipes have stopped.
		s.process.Cancel()
		<-s.exited
	}
	if err != nil {
		s.emit(proto.TypeError, proto.ErrorPayload{Error: err.Error()})
	}
	s.emit(proto.TypeDone, outcome)
	close(s.out)
	close(s.finished)
	s.executor.mu.Lock()
	s.connection.setCurrent(nil)
	if !reusable {
		s.executor.invalid = true
	}
	s.executor.active = nil
	s.settlement = agent.TurnSettlement{Reusable: reusable}
	s.settlementErr = err
	if !reusable {
		s.settlement.Reason = "native Turn did not establish reusable settlement"
	}
	close(s.settled)
	s.executor.mu.Unlock()
	s.outputCancel()
}

func (s *Session) captureSubagentBaseline() error {
	if !s.req.StrictResume || s.req.DisableSubagents {
		return nil
	}
	snapshot, err := s.readSubagents(s.ctx)
	s.subagentHistoryReady = err == nil
	s.previousNativeTurns = map[string]bool{}
	for _, session := range snapshot.Sessions {
		if session.ID == s.sessionID {
			for _, turn := range session.Turns {
				s.previousNativeTurns[turn.ID] = true
			}
		}
	}
	return err
}

func (s *Session) AwaitSettlement(ctx context.Context) (agent.TurnSettlement, error) {
	if s.settled == nil {
		return agent.TurnSettlement{}, fmt.Errorf("mcode: Turn has no Executor owner")
	}
	select {
	case <-s.settled:
		return s.settlement, s.settlementErr
	case <-ctx.Done():
		return agent.TurnSettlement{}, ctx.Err()
	}
}

func (s *Session) cancelTurn(ctx context.Context) error {
	e := s.executor
	e.mu.Lock()
	if e.active != s {
		e.mu.Unlock()
		_, err := s.AwaitSettlement(ctx)
		return err
	}
	s.mu.Lock()
	first := !s.cancelled && !s.closing
	s.cancelled = true
	if first {
		s.operations.Add(1)
	}
	s.mu.Unlock()
	// Cancellation releases event backpressure but does not cancel native owner
	// context. After native settlement, cancellation retires and drains the owner.
	s.outputCancel()
	e.mu.Unlock()
	var err error
	if first {
		raw, _ := json.Marshal(map[string]string{"sessionId": s.sessionID})
		err = s.writeContext(ctx, rpcFrame{JSONRPC: "2.0", Method: "session/cancel", Params: raw})
	}
	if first {
		if err == nil && s.req.StrictResume && !s.req.DisableSubagents {
			err = s.stopSubagents(ctx)
		}
		if err != nil {
			s.markInputUncertain()
		}
		s.operations.Done()
	}
	if err != nil {
		return err
	}
	_, err = s.AwaitSettlement(ctx)
	return err
}
