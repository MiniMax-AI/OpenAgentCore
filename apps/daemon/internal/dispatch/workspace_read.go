package dispatch

import (
	"context"
	"errors"
	"io/fs"
	"strings"
	"time"

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
	environment, code := r.workspaceResourceLocked(env.Assignment, request)
	if code != "" {
		r.mu.Unlock()
		return r.sendWorkspaceRead(ctx, env, rejectedWorkspaceRead(code))
	}
	if r.workspaceReads == nil {
		r.workspaceReads = make(map[string]string)
	}
	r.workspaceReads[env.ID] = env.Assignment.SessionID
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
		result := listWorkspaceDirectory(operation, environment, request)
		_ = r.sendWorkspaceRead(context.WithoutCancel(ctx), env, result)
	}()
	return nil
}

// workspaceResourceLocked returns the Environment owner of ref's Session once
// ref admits the read-only preparation's ready handle.
func (r *Router) workspaceResourceLocked(ref proto.AssignmentRef, request proto.WorkspaceReadPayload) (Environment, string) {
	if code := r.admitLocked(ref, ref.SessionID, request.EnvironmentID); code != "" {
		return nil, code
	}
	environment := r.assignments[ref.SessionID].environment
	if environment == nil {
		return nil, "read_unsupported"
	}
	p := r.preparations[request.Handle]
	if p == nil || p.request.Assignment != ref || p.environmentID != request.EnvironmentID || p.status.State != "ready" ||
		p.executor != nil || !p.owns || p.busy || p.ctx.Err() != nil || !time.Now().Before(p.deadline) {
		return nil, "resource_unavailable"
	}
	return environment, ""
}

func listWorkspaceDirectory(ctx context.Context, environment Environment, request proto.WorkspaceReadPayload) proto.WorkspaceReadResultPayload {
	read, err := environment.ListWorkspaceDirectory(ctx, request.Path, request.MaxEntries)
	if err != nil {
		return workspaceReadFailure(err)
	}
	if read.Entries == nil || len(read.Entries) > request.MaxEntries {
		return workspaceReadFailure(ErrWorkspaceReadUncertain)
	}
	directory := &proto.WorkspaceDirectoryResult{Entries: make([]proto.WorkspaceDirectoryEntry, 0, len(read.Entries)), Truncated: read.Truncated}
	for _, entry := range read.Entries {
		directory.Entries = append(directory.Entries, proto.WorkspaceDirectoryEntry{Name: entry.Name, Kind: entry.Kind, SizeBytes: entry.SizeBytes})
	}
	if !proto.ValidWorkspaceDirectory(directory, request.MaxEntries) {
		return workspaceReadFailure(ErrWorkspaceReadUncertain)
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
		{ErrWorkspaceReadUnavailable, "resource_unavailable"},
		{ErrWorkspaceReadInvalid, "invalid_request"},
		{ErrWorkspaceNotDirectory, proto.WorkspaceReadNotDirectory},
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
