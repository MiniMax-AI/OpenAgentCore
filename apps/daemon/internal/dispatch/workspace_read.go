package dispatch

import (
	"context"
	"errors"
	"io/fs"
	"strings"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

const workspaceReadCapacity = 4

func (r *Router) handleWorkspaceRead(ctx context.Context, env proto.Envelope) error {
	// Never echo an unbounded correlation ID onto the shared connection.
	if len(env.ID) > proto.WorkspaceReadMaxIDBytes {
		return errors.New("dispatch: invalid workspace read ID")
	}
	var request proto.WorkspaceReadPayload
	if len(env.Payload) > proto.WorkspaceReadMaxRequestBytes || env.DecodeRequest(&request) != nil || strings.TrimSpace(env.ID) == "" ||
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
	lister, code := r.workspaceResourceLocked(env.Assignment, request)
	if code != "" {
		r.mu.Unlock()
		return r.sendWorkspaceRead(ctx, env, rejectedWorkspaceRead(code))
	}
	if r.workspaceReads == nil {
		r.workspaceReads = make(map[string]struct{})
	}
	r.workspaceReads[env.ID] = struct{}{}
	done := r.trackWorkLocked(env.Assignment)
	r.shutdownWG.Add(1)
	r.mu.Unlock()
	go func() {
		defer r.shutdownWG.Done()
		defer done()
		defer func() { r.mu.Lock(); delete(r.workspaceReads, env.ID); r.mu.Unlock() }()
		// Observer loss does not discard an admitted native wait or replay it.
		operation, cancel := context.WithTimeout(context.WithoutCancel(ctx), 12*time.Second)
		defer cancel()
		result := listWorkspaceDirectory(operation, lister, request)
		_ = r.sendWorkspaceRead(context.WithoutCancel(ctx), env, result)
	}()
	return nil
}

// workspaceResourceLocked returns what lists ref's directory: the local
// workspace, or without one the Harness Session of the Run ref admitted.
// Only the local workspace serves a preparation handle; a read-only
// preparation needs it.
func (r *Router) workspaceResourceLocked(ref proto.AssignmentRef, request proto.WorkspaceReadPayload) (agent.WorkspaceDirectoryLister, string) {
	if code := r.admitLocked(ref, ref.SessionID, request.EnvironmentID); code != "" {
		return nil, code
	}
	if request.Handle != "" {
		p := r.preparations[request.Handle]
		if p == nil || p.request.Assignment != ref || p.environmentID != request.EnvironmentID || p.status.State != "ready" ||
			!p.owns || p.busy || p.ctx.Err() != nil || !time.Now().Before(p.deadline) || r.localWorkspace == nil {
			return nil, "resource_unavailable"
		}
		return r.localWorkspace, ""
	}
	s := r.sessions[request.RunID]
	if s == nil || s.assignment != ref || s.environmentID != request.EnvironmentID || s.session == nil ||
		!r.interactionRouteOpenLocked(s) {
		return nil, "resource_unavailable"
	}
	if r.localWorkspace != nil {
		return r.localWorkspace, ""
	}
	lister, ok := s.session.(agent.WorkspaceDirectoryLister)
	if !ok {
		return nil, "read_unsupported"
	}
	return lister, ""
}

func listWorkspaceDirectory(ctx context.Context, lister agent.WorkspaceDirectoryLister, request proto.WorkspaceReadPayload) proto.WorkspaceReadResultPayload {
	read, err := lister.ListWorkspaceDirectory(ctx, request.Path, request.MaxEntries)
	if err != nil {
		return workspaceReadFailure(err)
	}
	if read.Entries == nil || len(read.Entries) > request.MaxEntries {
		return workspaceReadFailure(agent.ErrWorkspaceReadUncertain)
	}
	directory := &proto.WorkspaceDirectoryResult{Entries: make([]proto.WorkspaceDirectoryEntry, 0, len(read.Entries)), Truncated: read.Truncated}
	for _, entry := range read.Entries {
		directory.Entries = append(directory.Entries, proto.WorkspaceDirectoryEntry{Name: entry.Name, Kind: entry.Kind, SizeBytes: entry.SizeBytes})
	}
	if !proto.ValidWorkspaceDirectory(directory, request.MaxEntries) {
		return workspaceReadFailure(agent.ErrWorkspaceReadUncertain)
	}
	return proto.WorkspaceReadResultPayload{Outcome: "completed", Directory: directory, CloseAcknowledged: true}
}

func rejectedWorkspaceRead(code string) proto.WorkspaceReadResultPayload {
	return proto.WorkspaceReadResultPayload{Outcome: "rejected", ErrorCode: code}
}

func workspaceReadFailure(err error) proto.WorkspaceReadResultPayload {
	for _, failure := range []struct {
		err  error
		code string
	}{
		{agent.ErrWorkspaceReadUnsupported, "read_unsupported"},
		{agent.ErrWorkspaceReadUnavailable, "resource_unavailable"},
		{agent.ErrWorkspaceReadBusy, "read_capacity"},
		{agent.ErrWorkspaceReadInvalid, "invalid_request"},
		{agent.ErrWorkspaceNotDirectory, proto.WorkspaceReadNotDirectory},
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
	if len(request.Trace) > 256 {
		request.Trace = ""
	}
	return r.reply(ctx, request, proto.TypeWorkspaceReadResult, result)
}
