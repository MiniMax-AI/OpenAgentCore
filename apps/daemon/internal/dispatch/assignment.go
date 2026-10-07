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
// protects it. A released assignment stays recorded, so its frames stay fenced.
type assignmentState struct {
	ref           proto.AssignmentRef
	environmentID string
	// resource and grant are the bind's Link resource and attach grant; the
	// resource's Kind is empty when the bind carried none.
	resource sandboxbootstrap.Resource
	grant    []byte
	released bool
	// work counts the Session's admitted reads, writes, exports and Runtime
	// preparations until each has sent its terminal result. A release waits
	// for it, and the released assignment admits no more.
	work sync.WaitGroup
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
		code = "invalid_request"
	} else {
		var resource sandboxbootstrap.Resource
		if input.Resource != nil {
			resource = *input.Resource
		}
		r.mu.Lock()
		a := r.assignments[ref.SessionID]
		switch {
		case a == nil:
			r.assignments[ref.SessionID] = &assignmentState{ref: ref, environmentID: input.EnvironmentID, resource: resource, grant: input.AttachGrant}
		case a.ref.AssignmentID == ref.AssignmentID && (ref.Epoch < a.ref.Epoch || ref.Epoch == a.ref.Epoch && a.released):
			code = proto.AssignmentStale
		case a.ref != ref || a.environmentID != input.EnvironmentID || a.resource != resource || !bytes.Equal(a.grant, input.AttachGrant):
			code = proto.AssignmentConflict
		}
		r.mu.Unlock()
	}
	return r.reply(ctx, env, proto.TypeAssignmentStatus, assignmentStatus(proto.AssignmentBound, code))
}

// handleAssignmentRelease fences the assignment, then settles the Session's
// work and Executor and removes its home before it replies. A retry at the
// same epoch repeats the cleanup.
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
		a.ref, a.released = ref, true
	}
	if code != "" {
		r.mu.Unlock()
		return r.reply(ctx, env, proto.TypeAssignmentStatus, assignmentStatus("", code))
	}
	preparations := r.fenceSessionWorkLocked(ref.SessionID)
	work := &r.assignments[ref.SessionID].work
	r.shutdownWG.Add(1)
	r.mu.Unlock()
	go func() {
		defer r.shutdownWG.Done()
		for _, p := range preparations {
			r.releasePreparation(p, "failed", proto.AssignmentStale, true)
		}
		work.Wait()
		state, code := proto.AssignmentReleased, ""
		err := r.closeSessionExecutor(ref.SessionID)
		if err == nil && input.RemoveHome {
			state, err = proto.AssignmentHomeRemoved, r.removeHome(ref.SessionID)
		}
		if err != nil {
			r.log.Warn("assignment release cleanup unconfirmed", "session_id", ref.SessionID, "err", err)
			code = proto.CleanupUnconfirmed
		}
		sendCtx, stop := r.shutdownContext(context.WithoutCancel(ctx))
		defer stop()
		_ = r.reply(sendCtx, env, proto.TypeAssignmentStatus, assignmentStatus(state, code))
	}()
	return nil
}

// fenceSessionWorkLocked ends the Session's transfers: one still receiving
// its body ends without applying it, and one that already committed checks the
// released assignment before it applies. It returns the read-only preparations
// the release releases, which also cancels their exports. Router.mu must be
// held.
func (r *Router) fenceSessionWorkLocked(sessionID string) []*preparationState {
	if u := r.workspaceWrite; u != nil && u.envelope.Assignment.SessionID == sessionID && !u.finished {
		u.finished = true
		close(u.ready)
	}
	if u := r.runtimePreparation; u != nil && u.envelope.Assignment.SessionID == sessionID && !u.finished {
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
