package dispatch

import (
	"context"
	"errors"
	"strings"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

var ErrRouterQuiesced = errors.New("dispatch: router quiesced")
var ErrRouterBusy = errors.New("dispatch: router has unsettled work")

// AssignmentError rejects a frame whose assignment does not admit it. Its
// value is the error code.
type AssignmentError string

func (e AssignmentError) Error() string { return "dispatch: " + string(e) }

// Quiesce serializes against admission, then drains every admitted output and
// receipt before acknowledging suspension. Busy rejection leaves admission open;
// a drain timeout keeps it closed until the caller shuts the connection down.
// ref must admit work in the suspended Environment.
func (r *Router) Quiesce(ctx context.Context, ref proto.AssignmentRef, request proto.EnvironmentSuspendPayload) error {
	if strings.TrimSpace(request.EnvironmentID) == "" || strings.TrimSpace(request.SuspendID) == "" || len(request.SuspendID) > 128 {
		return errors.New("dispatch: invalid suspension identity")
	}
	if !r.admission.TryLock() {
		return ErrRouterBusy
	}
	defer r.admission.Unlock()
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return ErrRouterClosed
	}
	if r.suspension != nil {
		r.mu.Unlock()
		return ErrRouterQuiesced
	}
	if code := r.admitLocked(ref, ref.SessionID, request.EnvironmentID); code != "" {
		r.mu.Unlock()
		return AssignmentError(code)
	}
	if r.runtimePreparation != nil || len(r.sessions) != 0 || len(r.workspaceReads) != 0 || r.workspaceWrite != nil || r.workspaceExport != nil || len(r.permIndex) != 0 || len(r.askIndex) != 0 {
		r.mu.Unlock()
		return ErrRouterBusy
	}
	for _, p := range r.preparations {
		if p.owns || p.busy {
			r.mu.Unlock()
			return ErrRouterBusy
		}
	}
	for _, owner := range r.executors {
		if owner.invalid || owner.preparing || owner.run != nil || owner.admission != nil || owner.environmentID != request.EnvironmentID {
			r.mu.Unlock()
			return ErrRouterBusy
		}
	}
	r.suspension, r.suspendedBy = &request, ref
	for _, p := range r.preparations {
		if p.timer != nil {
			p.timer.Stop()
		}
	}
	for _, owner := range r.executors {
		owner.idleLease++
		if owner.timer != nil {
			owner.timer.Stop()
		}
	}
	r.mu.Unlock()
	err := r.shutdownWG.waitContext(ctx)
	r.mu.Lock()
	if r.closed {
		err = ErrRouterClosed
	}

	r.mu.Unlock()
	return err
}

// Resume opens admission only after the caller authenticated a new connection
// and Core confirmed the exact suspension identity on that connection under the
// assignment that quiesced.
func (r *Router) Resume(ref proto.AssignmentRef, request proto.EnvironmentSuspendPayload, sender Sender) error {
	r.admission.Lock()
	defer r.admission.Unlock()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return ErrRouterClosed
	}
	if sender == nil || r.suspension == nil || !r.suspension.SameSuspension(request) {
		return errors.New("dispatch: suspension identity mismatch")
	}
	if ref != r.suspendedBy {
		return AssignmentError(proto.AssignmentConflict)
	}
	r.sender = sender
	r.suspension, r.suspendedBy = nil, proto.AssignmentRef{}
	for _, owner := range r.executors {
		r.scheduleExecutorIdleLocked(owner)
	}
	return nil
}
