package codex

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

// Executor owns the prepared process, fixed plan and native thread. Each StartTurn
// creates independent receipt, observation, cancellation and output ownership.
type Executor struct {
	mu                           sync.Mutex
	base                         *Session
	plan                         SessionPlan
	resumeID                     string
	requireExistingNativeSession bool
	active                       *Session
	closed                       bool
	closeMu                      sync.Mutex
}

func PrepareExecutor(ctx context.Context, req proto.PromptRequestPayload) (agent.Executor, error) {
	e, err := newExecutor(ctx, req, defaultSessionConfig())
	if e == nil {
		return nil, err
	}
	return e, err
}

func (e *Executor) StartTurn(ctx context.Context, runID string, input proto.MessageInput, out chan<- proto.Envelope) (agent.Turn, error) {
	if out == nil || strings.TrimSpace(runID) == "" || input.Validate() != nil {
		return nil, errors.New("codex: start requires a run identity, input and output")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	e.mu.Lock()
	base := e.base
	if e.closed || base.cancelCtx.Err() != nil || !base.rpc.Alive() {
		e.mu.Unlock()
		return nil, errors.New("codex: executor unavailable")
	}
	previous := e.active
	if previous != nil {
		select {
		case <-previous.waitDone:
		default:
			e.mu.Unlock()
			return nil, errors.New("codex: previous turn has not settled")
		}
		if previous.settlementErr != nil || !previous.settlement.Reusable {
			e.mu.Unlock()
			return nil, errors.New("codex: executor requires retirement")
		}
	}
	functions := &functionCalls{definitions: base.functions.definitions, names: base.functions.names, pending: map[string]*pendingFunction{}}
	turnCtx, cancel := context.WithCancel(base.cancelCtx)
	s := &Session{nativeHome: base.nativeHome,
		functions: functions, observeSubagentIdentities: base.observeSubagentIdentities,
		cfg: base.cfg, rpc: base.rpc, cancelCtx: turnCtx, cancelFn: cancel,
		waitDone: make(chan struct{}), outputDone: make(chan struct{}),
		bufs: NewItemBuffers(), resolvedModel: base.resolvedModel, runID: runID, out: out}
	if previous != nil {
		s.threadID = previous.currentThreadID()
		s.retiredTurns = make(map[string]bool, len(previous.retiredTurns)+1)
		for id := range previous.retiredTurns {
			s.retiredTurns[id] = true
		}
		previous.steering.mu.Lock()
		s.retiredTurns[previous.steering.id] = true
		previous.steering.mu.Unlock()
		s.resolvedModel = previous.resolvedModel
		previous.usageMu.Lock()
		s.usageTotal = previous.usageTotal
		previous.usageMu.Unlock()
	}
	e.active = s
	if s.observeSubagentIdentities {
		s.startSubagentObservations()
	}
	s.registerHandlers()
	e.mu.Unlock()
	// Ownership precedes any native submission. Even an uncertain start returns the
	// exact Turn so its caller can await settlement without replaying the input.
	var err error
	if s.currentThreadID() == "" {
		err = s.resolveThread(proto.PromptRequestPayload{AgentSessionID: e.resumeID, RequireExistingNativeSession: e.requireExistingNativeSession}, e.plan)
	}
	var native []UserInput
	if err == nil {
		native, err = nativeInput(input)
	}
	model := strings.TrimSpace(s.resolvedModel)
	if err == nil && model == "" {
		err = errors.New("codex: collaboration mode requires a resolved model")
	}
	if err == nil {
		var developerInstructions *string
		if instructions := e.plan.SystemPrompt; instructions != "" {
			developerInstructions = &instructions
		}
		params := TurnStartParams{ThreadID: s.currentThreadID(), Input: native, CollaborationMode: &CollaborationMode{
			Mode:     CollaborationModeDefault,
			Settings: CollaborationModeSettings{ReasoningEffort: e.plan.ModelReasoningEffort, Model: model, DeveloperInstructions: developerInstructions},
		}}
		startCtx, stop := context.WithTimeout(ctx, 10*time.Second)
		_, err = s.rpc.requestWithResult(startCtx, "turn/start", params, s.bindTurnResult)
		stop()
		if err != nil {
			s.cfg.logger.Warn("codex: turn/start ack failed", "run_id", runID, "err", err)
			err = fmt.Errorf("codex: turn/start: %w", err)
		}
	}
	if err != nil {
		s.emitTerminal("codex: native start failed")
	}
	go s.settleExecutorTurn(err)
	return s, err
}

func (e *Executor) Close(ctx context.Context) error {
	e.mu.Lock()
	e.closed = true
	e.mu.Unlock()
	done := make(chan error, 1)
	go func() {
		e.closeMu.Lock()
		defer e.closeMu.Unlock()
		base := e.base
		e.mu.Lock()
		running := e.active
		e.mu.Unlock()
		if running != nil {
			select {
			case <-running.waitDone:
			default:
				// Try native cancellation before closing its transport, but never
				// let an absent terminal receipt prevent resource retirement.
				cancelCtx, stop := context.WithTimeout(ctx, 10*time.Second)
				_ = running.Cancel(cancelCtx)
				stop()
			}
		}
		if running != nil && base.rpc.Alive() {
			// A caller deadline only stops waiting. Cleanup retains a bounded
			// opportunity to release native terminals before closing their owner.
			cleanupCtx, stop := context.WithTimeout(context.Background(), rpcKillTimeout)
			err := running.cleanupNativeTerminals(cleanupCtx)
			stop()
			if err != nil {
				// Preserve the native owner and exact handles for a later Close.
				done <- err
				return
			}
		}
		base.cancelFn()
		err := base.rpc.Close()
		e.mu.Lock()
		active := e.active
		e.mu.Unlock()
		if err == nil && active != nil {
			// A Turn's permanent outcome error survives Close. Cleanup succeeds
			// when its work and output have stopped, not when that outcome changes.
			select {
			case <-active.waitDone:
			case <-ctx.Done():
				err = ctx.Err()
			}
		}
		if err == nil {
			err = base.rpc.awaitReaders(ctx)
		}
		if err == nil {
			e.plan.Cleanup()
		}
		done <- err
	}()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

var _ agent.Executor = (*Executor)(nil)

func NewExecutorFactory() agent.ExecutorFactory { return PrepareExecutor }
