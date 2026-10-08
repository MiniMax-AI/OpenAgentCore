package dispatch

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

var ErrRouterQuiesced = errors.New("dispatch: router quiesced")
var ErrRouterBusy = errors.New("dispatch: router has unsettled work")

// AssignmentError rejects a frame whose assignment does not admit it. Its
// value is the error code.
type AssignmentError string

func (e AssignmentError) Error() string { return "dispatch: " + string(e) }

// quiesceTimeout bounds the drain of an environment_quiesce that Handle
// admitted.
const quiesceTimeout = 5 * time.Second

// suspension is one quiesced Environment: the request and the assignment
// that quiesced it, which its resume must carry.
type suspension struct {
	request proto.EnvironmentSuspendPayload
	by      proto.AssignmentRef
}

// Quiesce quiesces the request's Environment for a caller that then
// suspends the whole Runtime: after fencing the Environment it waits for every
// output and receipt admitted on the connection, then closes the
// Environment's Executors and owners before it returns. Busy rejection leaves
// admission open; a failed drain keeps the Environment quiesced until a
// matching resume or shutdown. ref must admit work in the Environment.
func (r *Router) Quiesce(ctx context.Context, ref proto.AssignmentRef, request proto.EnvironmentSuspendPayload) error {
	if err := r.fenceEnvironment(ref, request); err != nil {
		return err
	}
	if err := r.shutdownWG.waitContext(ctx); err != nil {
		return err
	}
	return r.drainEnvironment(ctx, request.EnvironmentID)
}

// Resume reopens the quiesced Environment and replaces the sender, after the
// caller authenticated a new connection and Core confirmed the exact
// suspension on it under the assignment that quiesced.
func (r *Router) Resume(ref proto.AssignmentRef, request proto.EnvironmentSuspendPayload, sender Sender) error {
	if sender == nil {
		return errors.New("dispatch: no sender")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.resumeLocked(ref, request); err != nil {
		return err
	}
	r.sender = sender
	return nil
}

// handleQuiesce quiesces an Environment for Core on this connection. It
// replies environment_quiesced once the drain settles, without blocking
// Handle; a failed drain replies resource_busy.
func (r *Router) handleQuiesce(ctx context.Context, env proto.Envelope) error {
	var request proto.EnvironmentSuspendPayload
	if env.ID == "" || len(env.ID) > 128 || env.DecodeRequest(&request) != nil || request.Rollback {
		return r.replySuspension(ctx, env, proto.TypeEnvironmentQuiesced, request, "invalid_request")
	}
	if err := r.fenceEnvironment(env.Assignment, request); err != nil {
		return r.replySuspension(ctx, env, proto.TypeEnvironmentQuiesced, request, suspensionCode(err))
	}
	r.shutdownWG.Add(1)
	go func() {
		defer r.shutdownWG.Done()
		drain, stop := r.shutdownContext(context.WithoutCancel(ctx))
		defer stop()
		drain, cancel := context.WithTimeout(drain, quiesceTimeout)
		defer cancel()
		code := ""
		if err := r.drainEnvironment(drain, request.EnvironmentID); err != nil {
			r.log.Warn("environment quiesce unconfirmed", "environment_id", request.EnvironmentID, "err", err)
			code = "resource_busy"
		}
		_ = r.replySuspension(context.WithoutCancel(ctx), env, proto.TypeEnvironmentQuiesced, request, code)
	}()
	return nil
}

// handleResume reopens the quiesced Environment that the resume names. A
// rollback of an Environment this connection has not quiesced is accepted
// and changes nothing.
func (r *Router) handleResume(ctx context.Context, env proto.Envelope) error {
	var request proto.EnvironmentSuspendPayload
	if env.ID == "" || len(env.ID) > 128 || env.DecodeRequest(&request) != nil {
		return r.replySuspension(ctx, env, proto.TypeEnvironmentResumed, request, "invalid_request")
	}
	r.mu.Lock()
	err := r.resumeLocked(env.Assignment, request)
	if errors.Is(err, errNotSuspended) && request.Rollback {
		err = nil
	}
	r.mu.Unlock()
	return r.replySuspension(ctx, env, proto.TypeEnvironmentResumed, request, suspensionCode(err))
}

var errNotSuspended = errors.New("dispatch: environment not suspended")

// resumeLocked reopens the Environment that request quiesced under ref.
// Router.mu must be held.
func (r *Router) resumeLocked(ref proto.AssignmentRef, request proto.EnvironmentSuspendPayload) error {
	s := r.suspensions[request.EnvironmentID]
	switch {
	case r.closed:
		return ErrRouterClosed
	case s == nil || !s.request.SameSuspension(request):
		return errNotSuspended
	case ref != s.by:
		return AssignmentError(proto.AssignmentConflict)
	}
	delete(r.suspensions, request.EnvironmentID)
	return nil
}

// fenceEnvironment quiesces the request's Environment once none of its
// Sessions has unsettled work: from then on Handle admits only their
// releases and the matching resume.
func (r *Router) fenceEnvironment(ref proto.AssignmentRef, request proto.EnvironmentSuspendPayload) error {
	if strings.TrimSpace(request.EnvironmentID) == "" || strings.TrimSpace(request.SuspendID) == "" || len(request.SuspendID) > 128 {
		return errors.New("dispatch: invalid suspension identity")
	}
	if !r.admission.TryLock() {
		return ErrRouterBusy
	}
	defer r.admission.Unlock()
	r.mu.Lock()
	defer r.mu.Unlock()
	switch {
	case r.closed:
		return ErrRouterClosed
	case r.suspensions[request.EnvironmentID] != nil:
		return ErrRouterQuiesced
	}
	if code := r.admitLocked(ref, ref.SessionID, request.EnvironmentID); code != "" {
		return AssignmentError(code)
	}
	in := func(sessionID string) bool {
		a := r.assignments[sessionID]
		return a != nil && a.environmentID == request.EnvironmentID
	}
	for session := range r.assignments {
		if in(session) && r.environmentTransferLocked(session) {
			return ErrRouterBusy
		}
	}
	for _, state := range r.sessions {
		if state.environmentID == request.EnvironmentID {
			return ErrRouterBusy
		}
	}
	for _, session := range r.workspaceReads {
		if in(session) {
			return ErrRouterBusy
		}
	}
	for _, p := range r.preparations {
		if (p.owns || p.busy) && p.environmentID == request.EnvironmentID {
			return ErrRouterBusy
		}
	}
	for _, owner := range r.executors {
		if owner.environmentID == request.EnvironmentID && (owner.invalid || owner.preparing || owner.run != nil || owner.admission != nil) {
			return ErrRouterBusy
		}
	}
	r.suspensions[request.EnvironmentID] = &suspension{request: request, by: ref}
	return nil
}

// drainEnvironment waits until the quiesced Environment's Sessions have sent
// every result they owe, then closes their Executors and releases their
// Environment owners.
func (r *Router) drainEnvironment(ctx context.Context, environmentID string) error {
	r.mu.Lock()
	var work []*dispatchWork
	for _, a := range r.assignments {
		if a.environmentID == environmentID {
			work = append(work, &a.work)
		}
	}
	var executors []*executorState
	for _, owner := range r.executors {
		if owner.environmentID == environmentID {
			owner.invalid, owner.closeReason = true, "quiesced"
			executors = append(executors, owner)
		}
	}
	r.mu.Unlock()
	for _, w := range work {
		if err := w.waitContext(ctx); err != nil {
			return err
		}
	}
	for _, owner := range executors {
		if err := r.closeExecutor(owner); err != nil {
			return err
		}
	}
	if err := r.closeEnvironments(ctx, environmentID); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return ErrRouterClosed
	}
	return nil
}

// rejectQuiesced answers a frame of a quiesced Environment, which admits only
// its Sessions' releases.
func (r *Router) rejectQuiesced(ctx context.Context, env proto.Envelope) error {
	return errors.Join(ErrRouterQuiesced, r.reply(ctx, env, proto.TypeProtocolError, proto.ProtocolErrorPayload{Type: env.Type, ErrorCode: "resource_unavailable"}))
}

// suspensionCode is the error code of a refused quiesce or resume.
func suspensionCode(err error) string {
	var rejected AssignmentError
	switch {
	case err == nil:
		return ""
	case errors.As(err, &rejected):
		return string(rejected)
	case errors.Is(err, errNotSuspended):
		return "not_suspended"
	}
	return "resource_busy"
}

func (r *Router) replySuspension(ctx context.Context, env proto.Envelope, typ string, request proto.EnvironmentSuspendPayload, code string) error {
	return r.reply(ctx, env, typ, proto.EnvironmentSuspendResultPayload{EnvironmentID: request.EnvironmentID, SuspendID: request.SuspendID, Accepted: code == "", ErrorCode: code})
}
