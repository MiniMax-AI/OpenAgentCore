package dispatch

import (
	"context"
	"errors"
	"io/fs"
	"strings"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/localworkspace"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

const workspaceReadCapacity = 4

func (r *Router) handleWorkspaceRead(ctx context.Context, env proto.Envelope) error {
	// Never echo an unbounded correlation ID onto the shared connection.
	if len(env.ID) > proto.WorkspaceReadMaxIDBytes {
		return errors.New("dispatch: invalid workspace read ID")
	}
	var request proto.WorkspaceReadPayload
	if len(env.Payload) > proto.WorkspaceReadMaxRequestBytes || env.DecodePayload(&request) != nil || strings.TrimSpace(env.ID) == "" ||
		!proto.ValidWorkspaceReadRequest(request) {
		return r.sendWorkspaceRead(ctx, env, rejectedWorkspaceRead("invalid_request"))
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return ErrRouterClosed
	}
	if _, exists := r.workspaceReads[env.ID]; exists {
		r.mu.Unlock()
		return errors.New("dispatch: workspace read already pending")
	}
	if len(r.workspaceReads) >= workspaceReadCapacity {
		r.mu.Unlock()
		return r.sendWorkspaceRead(ctx, env, rejectedWorkspaceRead("read_capacity"))
	}
	code := r.workspaceReadAdmissionLocked(request)
	if code != "" {
		r.mu.Unlock()
		return r.sendWorkspaceRead(ctx, env, rejectedWorkspaceRead(code))
	}
	if r.workspaceReads == nil {
		r.workspaceReads = make(map[string]struct{})
	}
	r.workspaceReads[env.ID] = struct{}{}
	binding := r.localWorkspace
	r.shutdownWG.Add(1)
	r.mu.Unlock()
	go func() {
		defer r.shutdownWG.Done()
		defer func() { r.mu.Lock(); delete(r.workspaceReads, env.ID); r.mu.Unlock() }()
		// Observer loss does not discard an admitted native wait or replay it.
		operation, cancel := context.WithTimeout(context.WithoutCancel(ctx), 12*time.Second)
		defer cancel()
		result := executeWorkspaceRead(operation, binding, request)
		_ = r.sendWorkspaceRead(context.WithoutCancel(ctx), env, result)
	}()
	return nil
}

func (r *Router) workspaceReadAdmissionLocked(request proto.WorkspaceReadPayload) string {
	if request.Handle != "" {
		p := r.preparations[request.Handle]
		if p == nil || p.environmentID != request.EnvironmentID || p.status.State != "ready" ||
			!p.owns || p.busy || p.ctx.Err() != nil || !time.Now().Before(p.deadline) {
			return "resource_unavailable"
		}
		return ""
	}
	s := r.sessions[request.RunID]
	if s == nil || s.environmentID != request.EnvironmentID || s.session == nil ||
		!r.interactionRouteOpenLocked(s) {
		return "resource_unavailable"
	}
	return ""
}

func executeWorkspaceRead(ctx context.Context, binding *localworkspace.Binding, request proto.WorkspaceReadPayload) proto.WorkspaceReadResultPayload {
	if request.Operation == "directory" {
		read, err := binding.ListWorkspaceDirectory(ctx, request.Path, request.MaxEntries)
		if err != nil {
			return workspaceReadResult(localworkspace.WorkspaceReadResult{}, err, 0)
		}
		return proto.WorkspaceReadResultPayload{Outcome: "completed", Directory: read, CloseAcknowledged: true}
	}
	read, err := binding.ReadWorkspaceFile(ctx, request.Path, request.MaxBytes)
	return workspaceReadResult(read, err, request.MaxBytes)
}

func rejectedWorkspaceRead(code string) proto.WorkspaceReadResultPayload {
	return proto.WorkspaceReadResultPayload{Outcome: "rejected", ErrorCode: code}
}

func workspaceReadResult(read localworkspace.WorkspaceReadResult, err error, limit int) proto.WorkspaceReadResultPayload {
	if err == nil && len(read.Data) <= limit && (!read.Truncated || len(read.Data) == limit) {
		return proto.WorkspaceReadResultPayload{Outcome: "completed", Data: read.Data, Truncated: read.Truncated, CloseAcknowledged: true}
	}
	for _, failure := range []struct {
		err  error
		code string
	}{
		{localworkspace.ErrWorkspaceReadUnavailable, "resource_unavailable"},
		{localworkspace.ErrWorkspaceReadBusy, "read_capacity"},
		{localworkspace.ErrWorkspaceReadInvalid, "invalid_request"},
		{localworkspace.ErrWorkspaceNotDirectory, proto.WorkspaceReadNotDirectory},
		{fs.ErrNotExist, "not_found"},
		{fs.ErrPermission, "permission_denied"},
	} {
		if errors.Is(err, failure.err) {
			return rejectedWorkspaceRead(failure.code)
		}
	}
	return proto.WorkspaceReadResultPayload{Outcome: "unknown", ErrorCode: "read_unconfirmed"}
}

func (r *Router) sendWorkspaceRead(ctx context.Context, request proto.Envelope, result proto.WorkspaceReadResultPayload) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	trace := request.Trace
	if len(trace) > 256 {
		trace = ""
	}
	env, err := proto.NewEnvelopeWithTrace(proto.TypeWorkspaceReadResult, request.ID, result, trace)
	if err != nil {
		return err
	}
	return r.sender.Send(ctx, env)
}
