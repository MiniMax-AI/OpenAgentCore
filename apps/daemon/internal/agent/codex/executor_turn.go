package codex

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
)

func (s *Session) AwaitSettlement(ctx context.Context) (agent.TurnSettlement, error) {
	select {
	case <-s.waitDone:
		return s.settlement, s.settlementErr
	case <-ctx.Done():
		return agent.TurnSettlement{}, ctx.Err()
	}
}

func (s *Session) settleExecutorTurn(startErr error) {
	defer close(s.waitDone)
	if startErr == nil {
		select {
		case <-s.outputDone:
		case <-s.rpc.Done():
			s.emitTerminal("codex: connection closed before settlement", true)
		case <-s.cancelCtx.Done():
			s.emitTerminal("codex: execution owner closed", true)
		}
	}
	// Detach the old callbacks before waiting for their captured Turn and native
	// replies. Later frames cannot acquire this Turn or redirect to a successor.
	if !s.nativeSettled.Load() || startErr != nil || !s.rpc.Alive() {
		s.cancelFn()
		s.settlementErr = errors.Join(s.settlementErr, s.rpc.Close())
	}
	if !s.nativeSettled.Load() {
		s.settlementErr = errors.Join(s.settlementErr, errors.New("codex: native Turn settlement is unconfirmed"))
	}
	s.rpc.detachHandlers()
	s.operationMu.Lock()
	s.operationsClosed = true
	s.operationMu.Unlock()
	s.stopSteering()
	s.stopFunctionCalls()
	s.stopCodexInteractionTimers()
	if !s.nativeSettled.Load() || startErr != nil || !s.rpc.Alive() {
		s.cancelFn()
		s.settlementErr = errors.Join(s.settlementErr, s.rpc.Close())
		s.settlement = agent.TurnSettlement{Reason: "native_execution_unavailable"}
	} else {
		s.settlement = agent.TurnSettlement{Reusable: true}
	}
	s.closeRunOutput()
	s.operations.Wait()
	if s.subagents != nil {
		s.subagents.mu.Lock()
		err := s.subagents.cancelResult
		s.subagents.mu.Unlock()
		if err != nil {
			s.settlement = agent.TurnSettlement{Reason: "child_settlement_unconfirmed"}
			s.settlementErr = errors.Join(s.settlementErr, err, s.rpc.Close())
		}
	}
	// The terminal callback has returned before detachHandlers completes. A
	// concurrent cancellation owns only this Turn and finishes before reuse.
	s.operationMu.Lock()
	ready := s.cancelReady
	s.operationMu.Unlock()
	if ready != nil {
		<-ready
		if s.cancelErr != nil {
			s.settlement.Reusable = false
			s.settlement.Reason = "cancellation_unconfirmed"
			s.settlementErr = errors.Join(s.settlementErr, s.cancelErr, s.rpc.Close())
		}
	}
	if s.cancelled.Load() && s.nativeSettled.Load() && s.cancelErr == nil && s.rpc.Alive() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
		err := s.cleanupNativeTerminals(cleanupCtx)
		stop()
		if err != nil {
			s.settlement = agent.TurnSettlement{Reason: "native_terminal_cleanup_unconfirmed"}
			s.settlementErr = errors.Join(s.settlementErr, err)
		}
	}
	s.cancelFn()
}

func (s *Session) beginOperation() bool {
	if s.executor == nil {
		return true
	}
	s.operationMu.Lock()
	defer s.operationMu.Unlock()
	if s.operationsClosed {
		return false
	}
	s.operations.Add(1)
	return true
}
func (s *Session) endOperation() {
	if s.executor != nil {
		s.operations.Done()
	}
}

func (s *Session) cancelExecutorTurn(ctx context.Context) error {
	s.operationMu.Lock()
	if s.operationsClosed {
		s.operationMu.Unlock()
		_, err := s.AwaitSettlement(ctx)
		return err
	}
	s.cancelOnce.Do(func() {
		s.cancelled.Store(true)
		s.cancelReady = make(chan struct{})
		go func() {
			defer close(s.cancelReady)
			s.stopFunctionCalls()
			turnID, active := s.stopSteering()
			s.stopCodexInteractionTimers()
			if !s.terminal.Load() && active {
				if turnID == "" {
					s.cancelErr = errors.New("codex: cancellation has no native turn identity")
					s.cancelFn()
					_ = s.rpc.Close()
					return
				}
				interruptCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				_, err := s.rpc.request(interruptCtx, "turn/interrupt", TurnInterruptParams{ThreadID: s.currentThreadID(), TurnID: turnID}, func(frame any) error { return s.rpc.writeFrameContext(interruptCtx, frame) })
				cancel()
				if err != nil {
					s.cancelErr = err
					s.cancelFn()
					_ = s.rpc.Close()
					return
				}
			}
			if s.subagents != nil {
				s.cancelErr = s.cancelSubagentWork(s.cancelCtx)
			}
		}()
	})
	s.operationMu.Unlock()
	_, err := s.AwaitSettlement(ctx)
	return err
}

var _ agent.Turn = (*Session)(nil)

// Server callbacks retain their originating Turn. MCP elicitation may be a
// standalone thread-scoped request in the native protocol; a supplied Turn ID
// must still match exactly.
func (s *Session) onServerRequest(method string, handler ServerRequestHandler) {
	s.rpc.OnServerRequest(method, func(raw json.RawMessage, id any) (any, error) {
		if s.executor != nil {
			var scope struct {
				ThreadID string `json:"threadId"`
				TurnID   string `json:"turnId"`
			}
			if json.Unmarshal(raw, &scope) != nil || !s.isRootThread(scope.ThreadID) || s.terminal.Load() ||
				(scope.TurnID != "" && !s.isRootTurn(scope.ThreadID, scope.TurnID)) ||
				(scope.TurnID == "" && method != "mcpServer/elicitation/request") {
				return nil, errors.New("codex: request does not belong to the active turn")
			}
		}
		if !s.beginOperation() {
			return nil, errors.New("codex: turn has settled")
		}
		defer s.endOperation()
		return handler(raw, id)
	})
}
