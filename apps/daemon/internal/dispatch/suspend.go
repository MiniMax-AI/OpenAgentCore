package dispatch

import (
	"context"
	"errors"
	"strings"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

var ErrRouterQuiesced = errors.New("dispatch: router quiesced")
var ErrRouterBusy = errors.New("dispatch: router has unsettled work")

// Quiesce serializes against admission, then drains every admitted output and
// receipt before acknowledging suspension. Busy rejection leaves admission open;
// a drain timeout keeps it closed until the caller shuts the connection down.
func (r *Router) Quiesce(ctx context.Context, request proto.EnvironmentSuspendPayload) error {
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
	for _, states := range r.idle {
		for state := range states {
			if state.environmentID != request.EnvironmentID || state.steerBusy || len(state.pendingIDs) != 0 || len(state.pendingAsks) != 0 {
				r.mu.Unlock()
				return ErrRouterBusy
			}
		}
	}
	for _, owner := range r.executors {
		if owner.invalid || owner.preparing || owner.run != nil || owner.admission != nil || owner.environmentID != request.EnvironmentID {
			r.mu.Unlock()
			return ErrRouterBusy
		}
	}
	r.suspension = &request
	for _, p := range r.preparations {
		if p.timer != nil {
			p.timer.Stop()
		}
	}
	for _, states := range r.idle {
		for state := range states {
			state.idleLease++
			if state.idleTimer != nil {
				state.idleTimer.Stop()
			}
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
// and Core confirmed the exact suspension identity on that connection.
func (r *Router) Resume(request proto.EnvironmentSuspendPayload, sender Sender) error {
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
	r.sender = sender
	r.suspension = nil
	for _, owner := range r.executors {
		r.scheduleExecutorIdleLocked(owner)
	}
	for _, states := range r.idle {
		for state := range states {
			r.scheduleIdleLocked(state)
		}
	}
	return nil
}
