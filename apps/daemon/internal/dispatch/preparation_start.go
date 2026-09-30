package dispatch

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

func (r *Router) handleExecutionStart(_ context.Context, env proto.Envelope) error {
	var input proto.ExecutionStartPayload
	if env.DecodePayload(&input) != nil || input.Handle == "" || input.ExecutorID == "" || strings.TrimSpace(input.RunID) == "" || input.Input.Validate() != nil {
		return r.rejectPreparation(env, "invalid_start")
	}
	encoded, _ := json.Marshal(input)
	fingerprint := sha256.Sum256(encoded)
	r.mu.Lock()
	if r.closed || r.suspension != nil {
		r.mu.Unlock()
		return ErrRouterClosed
	}
	if r.workspaceWrite != nil || r.workspaceExport != nil || r.runtimePreparation != nil {
		r.mu.Unlock()
		return r.rejectPreparation(env, "resource_unavailable")
	}
	p := r.preparations[input.Handle]
	if p == nil || p.requestID != env.ID {
		r.mu.Unlock()
		return r.rejectPreparation(env, "unknown_preparation")
	}
	if p.workspaceReadOnly {
		r.mu.Unlock()
		return r.rejectPreparation(env, "read_only_preparation")
	}
	owner := p.executor
	if owner == nil || owner.id != input.ExecutorID {
		r.mu.Unlock()
		return r.rejectPreparation(env, "unknown_executor")
	}
	if p.status.State == "starting" || p.status.State == "started" {
		matches, status := p.startFingerprint == fingerprint, p.status
		pending := p.handoff != nil && !p.handoff.published
		r.mu.Unlock()
		if !matches {
			return r.rejectPreparation(env, "start_conflict")
		}
		if pending {
			return nil
		}
		r.publishPreparation(p, status)
		return nil
	}
	if p.status.State != "ready" || p.ctx.Err() != nil || owner.admission != p || owner.run != nil || owner.invalid || owner.native == nil {
		r.mu.Unlock()
		return r.rejectPreparation(env, "preparation_not_ready")
	}
	if !time.Now().Before(p.deadline) {
		r.mu.Unlock()
		r.releasePreparation(p, "expired", "", true, true)
		return nil
	}
	if r.sessions[input.RunID] != nil {
		r.mu.Unlock()
		return r.rejectPreparation(env, "run_conflict")
	}
	p.status.State, p.status.RunID, p.status.Revision = "starting", input.RunID, p.status.Revision+1
	p.startFingerprint, p.busy = fingerprint, true
	state := &sessionState{capabilities: p.capabilities, runID: input.RunID, stateKey: p.stateKey, environmentID: p.environmentID, out: make(chan proto.Envelope, 64), ctx: p.ctx, ctxCancel: p.cancel, pendingIDs: make(map[string]struct{}), pendingAsks: make(map[string]struct{}), traceparent: env.Trace}
	state.preparedHandoff = newPreparedHandoff(p, owner.native)
	p.handoff, owner.run = state.preparedHandoff, state
	r.sessions[input.RunID] = state
	status := p.status
	r.shutdownWG.Add(2)
	r.mu.Unlock()
	go r.startPreparedExecution(p, state, input, status)
	return nil
}

func (r *Router) startPreparedExecution(p *preparationState, state *sessionState, input proto.ExecutionStartPayload, starting proto.PreparationStatusPayload) {
	defer r.shutdownWG.Done()
	handoff := state.preparedHandoff
	go r.forwardPreparedOutput(state)
	<-handoff.outputReady
	delivered := r.sendPreparation(p.requestID, p.trace, starting)
	r.mu.Lock()
	blocked := !delivered || handoff.release != nil && handoff.release.aborted() || p.ctx.Err() != nil
	r.mu.Unlock()
	var turn agent.Turn
	var startErr error
	if blocked {
		startErr = context.Canceled
	} else {
		turn, startErr = handoff.target.StartTurn(p.ctx, input.RunID, input.Input, state.out)
	}
	r.mu.Lock()
	handoff.turn = turn
	handoff.startErr = startErr
	p.timer.Stop()
	p.busy = false
	if turn != nil {
		p.status.State, p.status.ErrorCode, p.status.Revision = "started", "", p.status.Revision+1
	} else {
		p.status.State, p.status.ErrorCode, p.status.Revision = "failed", "executor_unavailable", p.status.Revision+1
	}
	status := p.status
	r.mu.Unlock()
	if turn == nil {
		close(state.out)
		code := "executor_unavailable"
		closeErr := r.closeExecutor(p.executor)
		if closeErr != nil {
			code = "executor_cleanup_unconfirmed"
		}
		if blocked {
			code = "start_cancelled"
		}
		r.mu.Lock()
		p.status.ErrorCode = code
		release, _ := r.claimPreparedReleaseLocked(state, true, "", false)
		close(handoff.startDone)
		r.mu.Unlock()
		if closeErr == nil {
			<-release.settled
		}
		status = proto.PreparationStatusPayload{Handle: p.status.Handle, ExecutorID: p.executor.id, State: "rejected", ErrorCode: code, Operation: proto.TypeExecutionStart}
		if delivered {
			r.sendPreparation(p.requestID, p.trace, status)
		}
		return
	}
	r.mu.Lock()
	aborted := handoff.release != nil && handoff.release.aborted()
	r.mu.Unlock()
	if aborted {
		status.State, status.ErrorCode = "failed", "start_cancelled"
	}
	delivered = delivered && !aborted && r.sendPreparationUntil(p.requestID, p.trace, status, p.deadline)
	r.mu.Lock()
	if !delivered && !aborted {
		handoff.outputErr = errors.Join(handoff.outputErr, errPreparedStatusDelivery)
	}
	handoff.published = turn != nil && delivered
	if handoff.published {
		p.owns = false
	}
	if handoff.published && handoff.release == nil {
		state.session = turn
	}
	if turn == nil || startErr != nil || !delivered {
		r.claimPreparedReleaseLocked(state, true, "prepared execution could not start", false)
	}
	close(handoff.startDone)
	r.mu.Unlock()
}
