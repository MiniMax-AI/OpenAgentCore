package dispatch

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/google/uuid"
)

const preparationCapacity = 4
const preparationRecords = 64

// All mutable fields are protected by Router.mu. owns includes resources whose
// cancellation is underway; a slow close cannot bypass the capacity bound.
type preparationState struct {
	capabilities proto.AgentKindCapabilities
	executor     *executorState
	// request is the execution_prepare's ID, trace and assignment, which
	// every status echoes.
	request           proto.Envelope
	fingerprint       [32]byte
	startFingerprint  [32]byte
	status            proto.PreparationStatusPayload
	deadline          time.Time
	timer             *time.Timer
	ctx               context.Context
	cancel            context.CancelFunc
	prepared          io.Closer
	environmentID     string
	busy              bool
	owns              bool
	closeErr          error
	workspaceReadOnly bool
	handoff           *preparedHandoff
}

func (r *Router) handleExecutionPrepare(ctx context.Context, env proto.Envelope) error {
	var input proto.ExecutionPreparePayload
	if env.DecodeRequest(&input) != nil || strings.TrimSpace(env.ID) == "" {
		return r.rejectPreparation(env, "invalid_request")
	}
	req := input.Configuration
	if !req.WorkspaceReadOnly {
		return r.handleExecutorPrepare(ctx, env, input)
	}
	caps, available := r.availableCapabilities(req.AgentKind)
	if !available {
		return r.rejectPreparation(env, "resource_unavailable")
	}
	if !caps.Preparation.IsSupported() {
		return r.rejectPreparation(env, "unsupported_preparation")
	}
	if !caps.WorkspaceReadPreparation.IsSupported() || !proto.ValidWorkspaceReadPreparation(req) {
		return r.rejectPreparation(env, "unsupported_read_preparation")
	}
	req, err := r.localWorkspace.Configure(req)
	if err != nil {
		return r.rejectPreparation(env, "invalid_configuration")
	}
	if req.RunID != "" || len(req.Input) != 0 || req.ConversationID != "" || req.EnvironmentID() == "" || strings.TrimSpace(req.AgentStateKey) == "" || !req.StrictResume || !req.ReleaseOnCompletion {
		return r.rejectPreparation(env, "invalid_configuration")
	}
	if validateExecutionEnvironment(req, caps) != nil || (len(req.FunctionTools) > 0 && !caps.FunctionTools.IsSupported()) {
		return r.rejectPreparation(env, "unsupported_configuration")
	}
	encoded, err := json.Marshal(req)
	if err != nil {
		return r.rejectPreparation(env, "invalid_configuration")
	}
	fingerprint := sha256.Sum256(encoded)
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return ErrRouterClosed
	}
	if code := r.admitLocked(env.Assignment, input.SessionID, req.EnvironmentID()); code != "" {
		r.mu.Unlock()
		return r.rejectPreparation(env, code)
	}
	if r.runtimePreparation != nil {
		r.mu.Unlock()
		return r.rejectPreparation(env, "resource_unavailable")
	}
	r.prunePreparationsLocked()
	if old := r.preparationRequests[env.ID]; old != nil {
		status := old.status
		matches := old.fingerprint == fingerprint
		r.mu.Unlock()
		if !matches {
			return r.rejectPreparation(env, "request_conflict")
		}
		r.publishPreparation(old, status)
		return nil
	}
	owned := 0
	for _, p := range r.preparations {
		if p.owns {
			owned++
		}
	}
	if owned >= preparationCapacity || len(r.preparations) >= preparationRecords {
		r.mu.Unlock()
		return r.rejectPreparation(env, "preparation_capacity")
	}
	owner, cancel := context.WithCancel(context.WithoutCancel(ctx))
	p := &preparationState{capabilities: caps, request: proto.Envelope{ID: env.ID, Trace: env.Trace, Assignment: env.Assignment}, fingerprint: fingerprint, ctx: owner, cancel: cancel, environmentID: req.EnvironmentID(), workspaceReadOnly: req.WorkspaceReadOnly, busy: true, owns: true, deadline: time.Now().Add(r.preparationTimeout)}
	p.status = proto.PreparationStatusPayload{Handle: uuid.NewString(), Revision: 1, State: "preparing", ExpiresAt: p.deadline.UnixMilli()}
	r.preparations[p.status.Handle], r.preparationRequests[p.request.ID] = p, p
	p.timer = time.AfterFunc(r.preparationTimeout, func() { r.releasePreparation(p, "expired", "", true, true) })
	r.shutdownWG.Add(1)
	r.mu.Unlock()
	go r.prepareExecution(p)
	return nil
}

// prepareExecution readies a read-only preparation without starting a Harness.
func (r *Router) prepareExecution(p *preparationState) {
	defer r.shutdownWG.Done()
	if !r.sendPreparation(p.request, proto.PreparationStatusPayload{Handle: p.status.Handle, Revision: 1, State: "preparing", ExpiresAt: p.deadline.UnixMilli()}) {
		r.releasePreparation(p, "failed", "status_delivery_failed", false, false)
	}
	r.mu.Lock()
	p.busy = false
	ready := p.status.State == "preparing" && p.ctx.Err() == nil && !r.closed
	if ready {
		p.prepared = localDirectoryPreparation{}
		p.status.State, p.status.Revision = "ready", p.status.Revision+1
	} else if p.status.State == "preparing" {
		p.status.State, p.status.ErrorCode, p.status.Revision = "failed", "preparation_failed", p.status.Revision+1
	}
	status := p.status
	if !ready {
		p.busy = true
		p.cancel()
		p.timer.Stop()
	}
	r.mu.Unlock()
	if !ready {
		r.closePreparationResource(p)
		return
	}
	if !r.sendPreparation(p.request, status) {
		r.releasePreparation(p, "failed", "status_delivery_failed", false, false)
	}
}

func (r *Router) handleExecutionRelease(_ context.Context, env proto.Envelope) error {
	var input proto.ExecutionReleasePayload
	if env.DecodeRequest(&input) != nil || input.Handle == "" {
		return r.rejectPreparation(env, "invalid_release")
	}
	r.mu.Lock()
	p := r.preparations[input.Handle]
	valid := p != nil && p.request.ID == env.ID && p.request.Assignment == env.Assignment
	r.mu.Unlock()
	if !valid {
		return r.rejectPreparation(env, "unknown_preparation")
	}
	r.releasePreparation(p, "released", "", true, true)
	return nil
}

func (r *Router) releasePreparation(p *preparationState, state, code string, publish, retryHandoff bool) {
	r.mu.Lock()
	if r.closed || r.suspension != nil {
		r.mu.Unlock()
		return
	}
	if p.executor != nil {
		r.mu.Unlock()
		r.abandonExecutorAdmission(p, state, code, publish)
		return
	}
	if p.handoff != nil {
		if active := r.sessions[p.status.RunID]; active != nil && active.preparedHandoff == p.handoff {
			if state == "expired" && p.handoff.published {
				r.mu.Unlock()
				return
			}
			switch p.status.State {
			case "preparing", "ready", "starting", "started":
				p.status.State, p.status.ErrorCode, p.status.Revision = state, code, p.status.Revision+1
				p.timer.Stop()
			}
			r.claimPreparedReleaseLocked(active, true, "", retryHandoff)
			status := p.status
			r.mu.Unlock()
			if publish {
				r.publishPreparation(p, status)
			}
			return
		}
	}
	closeResource := false
	switch p.status.State {
	case "preparing", "ready", "starting":
		p.status.State, p.status.ErrorCode, p.status.Revision = state, code, p.status.Revision+1
		p.cancel()
		p.timer.Stop()
	}
	if p.owns && !p.busy {
		if p.workspaceReadOnly && state == "released" && p.status.ErrorCode == "cleanup_unconfirmed" {
			p.status.State, p.status.ErrorCode, p.status.Revision = state, "", p.status.Revision+1
		}
		p.busy = true
		closeResource = true
		r.shutdownWG.Add(1)
	}
	status := p.status
	settled := !p.owns && !p.busy
	r.mu.Unlock()
	if closeResource {
		go func() { defer r.shutdownWG.Done(); r.closePreparationResource(p) }()
	}
	if publish && (!p.workspaceReadOnly || settled) {
		r.publishPreparation(p, status)
	}
}

func (r *Router) prunePreparationsLocked() {
	var oldest *preparationState
	for handle, p := range r.preparations {
		if p.owns {
			continue
		}
		if time.Now().After(p.deadline) {
			delete(r.preparations, handle)
			delete(r.preparationRequests, p.request.ID)
		} else if oldest == nil || p.deadline.Before(oldest.deadline) {
			oldest = p
		}
	}
	if len(r.preparations) >= preparationRecords && oldest != nil {
		delete(r.preparations, oldest.status.Handle)
		delete(r.preparationRequests, oldest.request.ID)
	}
}

func (r *Router) publishPreparation(p *preparationState, status proto.PreparationStatusPayload) {
	r.mu.Lock()
	if p.workspaceReadOnly {
		if status.Revision != p.status.Revision || (p.owns && (p.busy || status.State == "released" || status.State == "expired") && status.State != "preparing" && status.State != "ready") {
			r.mu.Unlock()
			return
		}
	}
	if r.closed || r.suspension != nil {
		r.mu.Unlock()
		return
	}
	r.shutdownWG.Add(1)
	r.mu.Unlock()
	go func() {
		defer r.shutdownWG.Done()
		// A failed terminal notification must not restart incomplete cleanup.
		if !r.sendPreparation(p.request, status) && (!p.workspaceReadOnly || status.State == "preparing" || status.State == "ready") {
			r.releasePreparation(p, "failed", "status_delivery_failed", false, false)
		}
	}()
}

func (r *Router) sendPreparation(request proto.Envelope, status proto.PreparationStatusPayload) bool {
	return r.sendPreparationUntil(request, status, time.Now().Add(5*time.Second))
}

func (r *Router) sendPreparationUntil(request proto.Envelope, status proto.PreparationStatusPayload, deadline time.Time) bool {
	ctx, stop := r.shutdownContext(context.Background())
	defer stop()
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	env, err := request.Reply(proto.TypePreparationStatus, status)
	return err == nil && r.sender.Send(ctx, env) == nil
}

func (r *Router) rejectPreparation(env proto.Envelope, code string) error {
	r.mu.Lock()
	if !r.closed {
		r.shutdownWG.Add(1)
		go func() {
			defer r.shutdownWG.Done()
			r.sendPreparation(env, proto.PreparationStatusPayload{State: "rejected", ErrorCode: code, Operation: env.Type})
		}()
	}
	r.mu.Unlock()
	return errors.New("dispatch: " + code)
}
