package dispatch

import (
	"bytes"
	"context"
	"sync"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxbootstrap"
)

// assignmentState is the Router's record of one Session's assignment. Router.mu
// protects it. A released assignment stays recorded, so its frames stay fenced;
// a confirmed release drops its owner, grant and resource.
type assignmentState struct {
	ref                proto.AssignmentRef
	environmentID      string
	workspaceDirectory string
	// resource and grant are the bind's Link resource and attach grant; the
	// resource's Kind is empty when the bind carried none.
	resource sandboxbootstrap.Resource
	grant    []byte
	// environment is the owner resolved from the bind, or nil.
	environment Environment
	released    bool
	// superseding is the bind that superseded the assignment at ref, held
	// released until the earlier epoch's work and owner have settled.
	superseding *proto.AssignmentBindPayload
	// work counts the Session's admitted reads, writes, exports, Runtime
	// preparations, Run terminals and cancellation receipts until each has
	// sent its terminal result. A release, a superseding bind and a quiesce
	// wait for it, and a released assignment admits no more.
	work dispatchWork
	// cleanup serializes release and supersede cleanups, so a retry never
	// closes the owner while an earlier Close runs.
	cleanup sync.Mutex
}

// admitLocked returns why ref admits no new work of sessionID in
// environmentID, or "". Router.mu must be held.
func (r *Router) admitLocked(ref proto.AssignmentRef, sessionID, environmentID string) string {
	a := r.assignments[ref.SessionID]
	switch {
	case !ref.Valid() || a == nil || a.ref.AssignmentID != ref.AssignmentID:
		return proto.AssignmentConflict
	case ref.Epoch < a.ref.Epoch || ref.Epoch == a.ref.Epoch && a.released:
		return proto.AssignmentStale
	case ref.Epoch != a.ref.Epoch || sessionID != ref.SessionID || environmentID != a.environmentID:
		return proto.AssignmentConflict
	}
	return ""
}

// trackWorkLocked counts work that ref admitted until the returned func runs,
// after the work has sent its terminal result. Router.mu must be held.
func (r *Router) trackWorkLocked(ref proto.AssignmentRef) func() {
	work := &r.assignments[ref.SessionID].work
	work.Add(1)
	return work.Done
}

// admittedEnvironment admits ref for the Session and returns the owner that
// its assignment resolved, or nil, or the assignment rejection code. A
// superseding bind replaces both once the earlier epoch's work has settled.
func (r *Router) admittedEnvironment(ref proto.AssignmentRef, sessionID string) (Environment, string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	a := r.assignments[ref.SessionID]
	if a == nil {
		return nil, proto.AssignmentConflict
	}
	if code := r.admitLocked(ref, sessionID, a.environmentID); code != "" {
		return nil, code
	}
	return a.environment, ""
}

// admitRunLocked admits a frame for the run, which ref must have started.
// Router.mu must be held.
func (r *Router) admitRunLocked(ref proto.AssignmentRef, state *sessionState) string {
	if state.assignment != ref {
		return proto.AssignmentConflict
	}
	return r.admitLocked(ref, ref.SessionID, state.environmentID)
}

func (r *Router) handleAssignmentBind(ctx context.Context, env proto.Envelope) error {
	var input proto.AssignmentBindPayload
	ref, code := env.Assignment, ""
	if env.ID == "" || env.DecodeRequest(&input) != nil || input.Validate() != nil || !ref.Valid() {
		return r.reply(ctx, env, proto.TypeAssignmentStatus, assignmentStatus(proto.AssignmentBound, "invalid_request"))
	}
	var resource sandboxbootstrap.Resource
	if input.Resource != nil {
		resource = *input.Resource
	}
	r.mu.Lock()
	if r.suspensions[input.EnvironmentID] != nil {
		r.mu.Unlock()
		return r.rejectQuiesced(ctx, env)
	}
	a := r.assignments[ref.SessionID]
	switch {
	case a == nil:
		var environment Environment
		if r.environments != nil {
			if environment = r.environments(ref, input); environment == nil {
				code = proto.AssignmentConflict
				break
			}
		}
		r.assignments[ref.SessionID] = &assignmentState{ref: ref, environmentID: input.EnvironmentID, workspaceDirectory: input.WorkspaceDirectory, resource: resource, grant: input.AttachGrant, environment: environment}
	case a.ref.AssignmentID != ref.AssignmentID:
		code = proto.AssignmentConflict
	case ref.Epoch > a.ref.Epoch && (!a.released || a.superseding != nil):
		// The bind supersedes the earlier epoch, or a pending supersede. A
		// Session's Environment and workspace never change, so conflicts are refused
		// before anything is fenced.
		if input.EnvironmentID != a.environmentID || input.WorkspaceDirectory != a.workspaceDirectory {
			code = proto.AssignmentConflict
			break
		}
		preparations := r.fenceSessionWorkLocked(ref.SessionID)
		a.ref, a.released, a.superseding = ref, true, &input
		r.shutdownWG.Add(1)
		r.mu.Unlock()
		go r.supersede(ctx, env, a, preparations)
		return nil
	case ref.Epoch == a.ref.Epoch && a.superseding != nil:
		if !sameBind(*a.superseding, input) {
			code = proto.AssignmentConflict
			break
		}
		// A retry repeats the pending supersede.
		r.shutdownWG.Add(1)
		r.mu.Unlock()
		go r.supersede(ctx, env, a, nil)
		return nil
	case ref.Epoch < a.ref.Epoch || ref.Epoch == a.ref.Epoch && a.released:
		code = proto.AssignmentStale
	case a.ref != ref || a.environmentID != input.EnvironmentID || a.workspaceDirectory != input.WorkspaceDirectory || a.resource != resource || !bytes.Equal(a.grant, input.AttachGrant):
		code = proto.AssignmentConflict
	}
	r.mu.Unlock()
	return r.reply(ctx, env, proto.TypeAssignmentStatus, assignmentStatus(proto.AssignmentBound, code))
}

// supersede settles the earlier epoch's work, closes the Session's Executor
// and owner, and then binds the pending supersede at env's assignment. It
// replies assignment_stale when a release or a later bind took over meanwhile.
// A failed cleanup keeps the supersede pending, so a retry repeats it.
func (r *Router) supersede(ctx context.Context, env proto.Envelope, a *assignmentState, preparations []*preparationState) {
	defer r.shutdownWG.Done()
	ref := env.Assignment
	cleanupCtx, stop := r.shutdownContext(context.WithoutCancel(ctx))
	defer stop()
	for _, p := range preparations {
		r.releasePreparation(p, "failed", proto.AssignmentStale, true)
	}
	a.work.Wait()
	a.cleanup.Lock()
	defer a.cleanup.Unlock()
	r.mu.Lock()
	pending, environment := a.ref == ref && a.superseding != nil, a.environment
	r.mu.Unlock()
	var err error
	if pending {
		err = r.closeSessionExecutor(ref.SessionID)
	}
	if pending && err == nil && environment != nil {
		err = environment.Close(cleanupCtx)
	}
	code := ""
	r.mu.Lock()
	switch {
	case a.ref != ref || a.released && a.superseding == nil:
		code = proto.AssignmentStale
	case a.superseding == nil:
		// An earlier attempt bound it.
	case err != nil:
		r.log.Warn("assignment supersede cleanup unconfirmed", "session_id", ref.SessionID, "err", err)
		code = proto.CleanupUnconfirmed
	default:
		input := *a.superseding
		var owner Environment
		if r.environments != nil {
			if owner = r.environments(ref, input); owner == nil {
				code = proto.AssignmentConflict
				break
			}
		}
		var resource sandboxbootstrap.Resource
		if input.Resource != nil {
			resource = *input.Resource
		}
		a.resource, a.grant, a.environment = resource, input.AttachGrant, owner
		a.released, a.superseding = false, nil
	}
	r.mu.Unlock()
	_ = r.reply(cleanupCtx, env, proto.TypeAssignmentStatus, assignmentStatus(proto.AssignmentBound, code))
}

func sameBind(a, b proto.AssignmentBindPayload) bool {
	return a.EnvironmentID == b.EnvironmentID && a.WorkspaceDirectory == b.WorkspaceDirectory && (a.Resource == nil) == (b.Resource == nil) && (a.Resource == nil || *a.Resource == *b.Resource) && bytes.Equal(a.AttachGrant, b.AttachGrant)
}

// handleAssignmentRelease fences the assignment, then settles the Session's
// work and Executor, closes its Environment owner and removes its home before
// it replies. A retry at the same epoch repeats the cleanup.
func (r *Router) handleAssignmentRelease(ctx context.Context, env proto.Envelope) error {
	var input proto.AssignmentReleasePayload
	ref, code := env.Assignment, ""
	if env.ID == "" || env.DecodeRequest(&input) != nil || !ref.Valid() {
		code = "invalid_request"
	} else if input.RemoveHome && r.removeHome == nil {
		code = proto.UnsupportedOperation
	}
	if code != "" {
		return r.reply(ctx, env, proto.TypeAssignmentStatus, assignmentStatus("", code))
	}
	r.mu.Lock()
	a := r.assignments[ref.SessionID]
	switch {
	case a == nil:
		r.assignments[ref.SessionID] = &assignmentState{ref: ref, released: true}
	case a.ref.AssignmentID != ref.AssignmentID || ref.Epoch == a.ref.Epoch && !a.released:
		code = proto.AssignmentConflict
	case ref.Epoch < a.ref.Epoch:
		code = proto.AssignmentStale
	default:
		a.ref, a.released, a.superseding = ref, true, nil
	}
	if code != "" {
		r.mu.Unlock()
		return r.reply(ctx, env, proto.TypeAssignmentStatus, assignmentStatus("", code))
	}
	preparations := r.fenceSessionWorkLocked(ref.SessionID)
	a = r.assignments[ref.SessionID]
	work, environment := &a.work, a.environment
	r.shutdownWG.Add(1)
	r.mu.Unlock()
	go func() {
		defer r.shutdownWG.Done()
		cleanupCtx, stop := r.shutdownContext(context.WithoutCancel(ctx))
		defer stop()
		for _, p := range preparations {
			r.releasePreparation(p, "failed", proto.AssignmentStale, true)
		}
		work.Wait()
		a.cleanup.Lock()
		defer a.cleanup.Unlock()
		state, code := proto.AssignmentReleased, ""
		err := r.closeSessionExecutor(ref.SessionID)
		if err == nil && environment != nil {
			err = environment.Close(cleanupCtx)
		}
		if err == nil && input.RemoveHome {
			state, err = proto.AssignmentHomeRemoved, r.removeHome(ref.SessionID)
		}
		if err != nil {
			r.log.Warn("assignment release cleanup unconfirmed", "session_id", ref.SessionID, "err", err)
			code = proto.CleanupUnconfirmed
		} else {
			r.mu.Lock()
			a.environment, a.grant, a.resource = nil, nil, sandboxbootstrap.Resource{}
			r.mu.Unlock()
		}
		_ = r.reply(cleanupCtx, env, proto.TypeAssignmentStatus, assignmentStatus(state, code))
	}()
	return nil
}

// fenceSessionWorkLocked ends the Session's transfers: one still receiving
// its body ends without applying it, and one that already committed checks the
// released assignment before it applies. It returns the read-only preparations
// the release releases, which also cancels their exports. Router.mu must be
// held.
func (r *Router) fenceSessionWorkLocked(sessionID string) []*preparationState {
	if u := r.workspaceWrites[sessionID]; u != nil && !u.finished {
		u.finished = true
		close(u.ready)
	}
	if u := r.runtimePreparations[sessionID]; u != nil && !u.finished {
		r.finishRuntimePreparationTransferLocked(u, false)
	}
	var preparations []*preparationState
	for _, p := range r.preparations {
		if p.executor == nil && p.owns && p.request.Assignment.SessionID == sessionID {
			preparations = append(preparations, p)
		}
	}
	return preparations
}

// closeSessionExecutor ends the Session's Executor: it abandons a pending
// admission, releases a Turn, waits for a native preparation and closes the
// Executor. The released assignment admits no new preparation meanwhile.
func (r *Router) closeSessionExecutor(sessionID string) error {
	r.mu.Lock()
	owner := r.executors[sessionID]
	if owner == nil {
		r.mu.Unlock()
		return nil
	}
	owner.invalid = true
	owner.closeReason = "assignment_released"
	if owner.preparing {
		owner.cancel()
	}
	var release *preparedRelease
	var attempt *preparedReleaseAttempt
	admission := owner.admission
	if owner.run != nil {
		release, attempt = r.claimPreparedReleaseLocked(owner.run, true, "", true)
		admission = nil
	}
	r.mu.Unlock()
	if admission != nil {
		r.abandonExecutorAdmission(admission, "failed", proto.AssignmentStale, true)
	}
	if release != nil {
		if err := r.awaitPreparedNativeRelease(context.Background(), release, attempt); err != nil {
			return err
		}
	}
	<-owner.prepared
	return r.closeExecutor(owner)
}

func assignmentStatus(state, code string) proto.AssignmentStatusPayload {
	if code != "" {
		state = proto.AssignmentFailed
	}
	return proto.AssignmentStatusPayload{State: state, ErrorCode: code}
}

// reply sends the reply to the request env within the send budget.
func (r *Router) reply(ctx context.Context, env proto.Envelope, typ string, payload any) error {
	reply, err := env.Reply(typ, payload)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return r.sender.Send(ctx, reply)
}
