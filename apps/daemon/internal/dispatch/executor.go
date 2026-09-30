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
	"github.com/google/uuid"
)

const executorIdleCapacity = 16

type executorState struct {
	capabilities                           proto.AgentKindCapabilities
	id, sessionID, environmentID, stateKey string
	fingerprint                            [32]byte
	native                                 agent.Executor
	nativeID                               string
	ctx                                    context.Context
	cancel                                 context.CancelFunc
	admission                              *preparationState
	run                                    *sessionState
	preparing                              bool
	invalid                                bool
	closeDone                              chan struct{}
	closeReason                            string
	closeErr                               error
	timer                                  *time.Timer
	idleLease                              uint64
}

func executorFingerprint(req proto.PromptRequestPayload) ([32]byte, error) {
	req.RunID, req.Input = "", nil
	req.AgentSessionID = ""
	req.RequireExistingNativeSession = false
	req.ReleaseOnCompletion = false
	data, err := json.Marshal(req)
	return sha256.Sum256(data), err
}

func (r *Router) handleExecutorPrepare(ctx context.Context, env proto.Envelope, input proto.ExecutionPreparePayload) error {
	req := input.Configuration
	if strings.TrimSpace(input.SessionID) == "" || req.RunID != "" || len(req.Input) != 0 || req.ConversationID != "" || req.WorkspaceAuthoring || req.AgentStateKey != "agents-api-"+input.SessionID || !req.StrictResume {
		return r.rejectPreparation(env, "invalid_configuration")
	}
	caps, available := r.availableCapabilities(req.AgentKind)
	if !available {
		return r.rejectPreparation(env, "resource_unavailable")
	}
	factory, err := r.registry.ResolveExecutor(req.AgentKind)
	if err != nil || !caps.Preparation.IsSupported() {
		return r.rejectPreparation(env, "unsupported_preparation")
	}
	req, err = r.localWorkspace.Configure(req)
	if err != nil {
		return r.rejectPreparation(env, "invalid_configuration")
	}
	if validateExecutionEnvironment(req, caps) != nil || len(req.FunctionTools) > 0 && !caps.FunctionTools.IsSupported() {
		return r.rejectPreparation(env, "unsupported_configuration")
	}
	fingerprint, err := executorFingerprint(req)
	if err != nil {
		return r.rejectPreparation(env, "invalid_configuration")
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		return r.rejectPreparation(env, "invalid_configuration")
	}
	requestFingerprint := sha256.Sum256(encoded)
	r.mu.Lock()
	if r.closed || r.suspension != nil {
		r.mu.Unlock()
		return ErrRouterClosed
	}
	if r.workspaceWrite != nil || r.workspaceExport != nil || r.runtimePreparation != nil {
		r.mu.Unlock()
		return r.rejectPreparation(env, "resource_unavailable")
	}
	r.prunePreparationsLocked()
	if old := r.preparationRequests[env.ID]; old != nil {
		matches, status := old.fingerprint == requestFingerprint, old.status
		r.mu.Unlock()
		if !matches {
			return r.rejectPreparation(env, "request_conflict")
		}
		r.publishPreparation(old, status)
		return nil
	}
	if len(r.preparations) >= preparationRecords {
		r.mu.Unlock()
		return r.rejectPreparation(env, "preparation_capacity")
	}
	active := 0
	for _, candidate := range r.executors {
		if candidate.sessionID != input.SessionID && candidate.stateKey == req.AgentStateKey {
			r.mu.Unlock()
			return r.rejectPreparation(env, "session_binding_conflict")
		}
	}
	for _, owner := range r.executors {
		if owner.preparing || owner.admission != nil || owner.run != nil || owner.invalid {
			active++
		}
	}
	if active >= preparationCapacity {
		r.mu.Unlock()
		return r.rejectPreparation(env, "preparation_capacity")
	}
	owner := r.executors[input.SessionID]
	reused := owner != nil
	if owner != nil {
		code := ""
		switch {
		case owner.environmentID != req.EnvironmentID() || owner.fingerprint != fingerprint:
			code = "configuration_conflict"
		case owner.invalid:
			code = "executor_cleanup_unconfirmed"
		case owner.preparing || owner.admission != nil || owner.run != nil:
			code = "resource_unavailable"
		case req.AgentSessionID != owner.nativeID:
			code = "native_session_conflict"
		case req.RequireExistingNativeSession && owner.nativeID == "":
			code = "history_unavailable"
		}
		if code != "" {
			r.mu.Unlock()
			return r.rejectPreparation(env, code)
		}
		if owner.timer != nil {
			owner.timer.Stop()
		}
		owner.idleLease++
	} else {
		if len(r.executors) >= executorIdleCapacity+preparationCapacity {
			r.mu.Unlock()
			return r.rejectPreparation(env, "executor_capacity")
		}
		ownerCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
		owner = &executorState{capabilities: caps, id: uuid.NewString(), sessionID: input.SessionID, environmentID: req.EnvironmentID(), stateKey: req.AgentStateKey, fingerprint: fingerprint, ctx: ownerCtx, cancel: cancel, preparing: true, nativeID: req.AgentSessionID}
		r.executors[input.SessionID] = owner
		r.log.Info("executor owner_created", "executor_id", owner.id, "session_id", owner.sessionID)
	}
	operation, cancel := context.WithCancel(context.WithoutCancel(ctx))
	p := &preparationState{capabilities: owner.capabilities, requestID: env.ID, trace: env.Trace, fingerprint: requestFingerprint, ctx: operation, cancel: cancel, stateKey: req.AgentStateKey, environmentID: req.EnvironmentID(), executor: owner, owns: true, busy: !reused, deadline: time.Now().Add(r.preparationTimeout)}
	state := "preparing"
	if reused {
		state = "ready"
	}
	p.status = proto.PreparationStatusPayload{Handle: uuid.NewString(), ExecutorID: owner.id, Revision: 1, State: state, Reused: reused, ExpiresAt: p.deadline.UnixMilli()}
	owner.admission = p
	r.preparations[p.status.Handle], r.preparationRequests[env.ID] = p, p
	p.timer = time.AfterFunc(r.preparationTimeout, func() { r.releasePreparation(p, "expired", "", true, true) })
	if !reused {
		r.shutdownWG.Add(1)
	}
	status := p.status
	r.mu.Unlock()
	if reused {
		r.publishPreparation(p, status)
	} else {
		go r.prepareExecutor(p, req, factory)
	}
	return nil
}

func (r *Router) prepareExecutor(p *preparationState, req proto.PromptRequestPayload, factory agent.ExecutorFactory) {
	defer r.shutdownWG.Done()
	owner := p.executor
	started := time.Now()
	r.mu.Lock()
	initial := p.status
	r.mu.Unlock()
	if !r.sendPreparation(p.requestID, p.trace, initial) {
		r.abandonExecutorAdmission(p, "failed", "status_delivery_failed", false)
	}
	var native agent.Executor
	var err error
	if owner.ctx.Err() == nil {
		req, err = r.localWorkspace.Prepare(owner.ctx, req)
		if err == nil && owner.ctx.Err() == nil {
			native, err = factory(owner.ctx, req)
		}
	} else {
		err = owner.ctx.Err()
	}
	r.mu.Lock()
	owner.native, owner.preparing = native, false
	r.log.Info("executor native_prepare", "executor_id", owner.id, "session_id", owner.sessionID, "duration_ms", time.Since(started).Milliseconds(), "success", err == nil && native != nil)
	ready := native != nil && err == nil && !owner.invalid && !r.closed && r.suspension == nil && p.status.State == "preparing" && p.ctx.Err() == nil
	p.busy = false
	if ready {
		p.status.State, p.status.Revision = "ready", p.status.Revision+1
	} else {
		owner.invalid = true
		owner.closeReason = "preparation_failed"
		if p.status.State == "preparing" {
			p.status.State, p.status.ErrorCode, p.status.Revision = "failed", "preparation_failed", p.status.Revision+1
		}
		p.timer.Stop()
	}
	status := p.status
	r.mu.Unlock()
	if !ready {
		closeErr := r.closeExecutor(owner)
		r.mu.Lock()
		p.owns, p.closeErr = closeErr != nil, closeErr
		if closeErr != nil {
			p.status.State, p.status.ErrorCode, p.status.Revision = "failed", "executor_cleanup_unconfirmed", p.status.Revision+1
		}
		status = p.status
		r.mu.Unlock()
	}
	if !r.sendPreparation(p.requestID, p.trace, status) && ready {
		r.abandonExecutorAdmission(p, "failed", "status_delivery_failed", false)
	}
}

func (r *Router) abandonExecutorAdmission(p *preparationState, state, code string, publish bool) {
	r.mu.Lock()
	owner := p.executor
	if p.handoff != nil || p.status.State == "started" {
		status := p.status
		r.mu.Unlock()
		if publish && state == "released" {
			r.publishPreparation(p, status)
		}
		return
	}
	if p.status.State == "preparing" || p.status.State == "ready" {
		p.status.State, p.status.ErrorCode, p.status.Revision = state, code, p.status.Revision+1
		p.timer.Stop()
		p.cancel()
		p.owns = false
		if owner.admission == p {
			owner.admission = nil
		}
		if owner.preparing {
			owner.invalid = true
			owner.cancel()
		} else if !owner.invalid {
			r.scheduleExecutorIdleLocked(owner)
		}
	}
	status := p.status
	r.mu.Unlock()
	if publish {
		r.publishPreparation(p, status)
	}
}

func (r *Router) scheduleExecutorIdleLocked(owner *executorState) {
	if owner.invalid || owner.preparing || owner.run != nil || owner.admission != nil {
		return
	}
	if owner.timer != nil {
		owner.timer.Stop()
	}
	idle := 0
	for _, candidate := range r.executors {
		if candidate != owner && candidate.run == nil && candidate.admission == nil && !candidate.preparing {
			idle++
		}
	}
	if idle >= executorIdleCapacity {
		owner.invalid = true
		owner.closeReason = "idle_capacity"
		r.shutdownWG.Add(1)
		go func() { defer r.shutdownWG.Done(); _ = r.closeExecutor(owner) }()
		return
	}
	owner.idleLease++
	lease := owner.idleLease
	r.log.Info("executor owner_idle", "executor_id", owner.id, "session_id", owner.sessionID)
	owner.timer = time.AfterFunc(r.idleTimeout, func() {
		r.mu.Lock()
		if r.executors[owner.sessionID] != owner || owner.idleLease != lease || owner.run != nil || owner.admission != nil || owner.invalid {
			r.mu.Unlock()
			return
		}
		owner.invalid = true
		owner.closeReason = "idle_expired"
		r.shutdownWG.Add(1)
		r.mu.Unlock()
		defer r.shutdownWG.Done()
		_ = r.closeExecutor(owner)
	})
}

// Close failures keep the exact owner; a later call retries only settled failures.
func (r *Router) closeExecutor(owner *executorState) error {
	r.mu.Lock()
	owner.invalid = true
	if owner.closeReason == "" {
		owner.closeReason = "invalidated"
	}
	if owner.timer != nil {
		owner.timer.Stop()
	}
	if owner.preparing {
		owner.cancel()
		r.mu.Unlock()
		return errors.New("executor preparation is still settling")
	}
	if done := owner.closeDone; done != nil {
		select {
		case <-done:
			if owner.closeErr == nil {
				r.mu.Unlock()
				return nil
			}
		default:
			r.mu.Unlock()
			<-done
			r.mu.Lock()
			err := owner.closeErr
			r.mu.Unlock()
			return err
		}
	}
	done := make(chan struct{})
	owner.closeDone = done
	native := owner.native
	r.mu.Unlock()
	var err error
	if native != nil {
		ctx, cancel := context.WithTimeout(context.Background(), preparedCancelTimeout)
		err = native.Close(ctx)
		cancel()
	}
	r.mu.Lock()
	owner.closeErr = err
	if err == nil {
		owner.cancel()
	}
	r.log.Info("executor owner_closed", "executor_id", owner.id, "session_id", owner.sessionID, "confirmed", err == nil, "reason", owner.closeReason)
	if err == nil && owner.run == nil && r.executors[owner.sessionID] == owner {
		delete(r.executors, owner.sessionID)
	}
	close(done)
	r.mu.Unlock()
	return err
}

func (r *Router) closeIdleExecutorsLocked() []*executorState {
	var owners []*executorState
	for _, owner := range r.executors {
		owner.invalid = true
		owner.closeReason = "shutdown"
		if owner.preparing {
			owner.cancel()
		}
		if owner.timer != nil {
			owner.timer.Stop()
		}
		if owner.admission != nil && owner.run == nil {
			p := owner.admission
			p.cancel()
			p.timer.Stop()
			p.status.State, p.status.ErrorCode, p.status.Revision = "failed", "connection_closed", p.status.Revision+1
			p.owns = false
			owner.admission = nil
		}
		if !owner.preparing && owner.run == nil {
			r.shutdownWG.Add(1)
			owners = append(owners, owner)
		}
	}
	return owners
}

func (r *Router) closeIdleExecutors(owners []*executorState) {
	for _, owner := range owners {
		go func() { defer r.shutdownWG.Done(); _ = r.closeExecutor(owner) }()
	}
}
