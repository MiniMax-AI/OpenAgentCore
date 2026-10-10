package dispatch

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

// ErrRouterQuiesced also wraps failed quiesce attempts that have fenced admission.
var ErrRouterQuiesced = errors.New("dispatch: router quiesced")
var ErrRouterBusy = errors.New("dispatch: router has unsettled work")

// Quiesce serializes against admission, then drains every admitted output and
// receipt and closes idle Executors before acknowledging suspension. Busy
// rejection leaves admission open; a failed close or drain timeout keeps it
// closed until the caller shuts the connection down.
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
	if r.runtimePreparation != nil || len(r.sessions) != 0 || len(r.workspaceReads) != 0 || r.workspaceWrite != nil || r.workspaceExport != nil {
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
	r.suspension = &request
	for _, p := range r.preparations {
		if p.timer != nil {
			p.timer.Stop()
		}
	}
	owners := r.closeIdleExecutorsLocked()
	for _, owner := range owners {
		owner.idleLease++
		owner.closeReason = "suspend"
	}
	r.mu.Unlock()
	r.closeIdleExecutors(owners)
	err := r.shutdownWG.waitContext(ctx)
	r.mu.Lock()
	if r.closed {
		err = ErrRouterClosed
	} else if err == nil {
		for _, owner := range r.executors {
			cause := owner.closeErr
			if cause == nil {
				cause = errors.New("cleanup has not settled")
			}
			err = errors.Join(err, fmt.Errorf("dispatch: executor %s: %w", owner.id, cause))
		}
	}

	r.mu.Unlock()
	if err != nil {
		return errors.Join(ErrRouterQuiesced, err)
	}
	return nil
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
	return nil
}
