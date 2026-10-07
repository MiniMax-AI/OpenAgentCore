package dispatch

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

type workspaceExport struct {
	request  proto.Envelope
	requests chan proto.WorkspaceExportPayload
	cancel   context.CancelFunc
}

func (r *Router) handleWorkspaceExport(ctx context.Context, env proto.Envelope) error {
	var request proto.WorkspaceExportPayload
	if len(env.ID) == 0 || len(env.ID) > proto.WorkspaceReadMaxIDBytes || len(env.Payload) > proto.WorkspaceReadMaxRequestBytes || env.DecodeRequest(&request) != nil || !proto.ValidWorkspaceExportRequest(request) {
		return errors.New("dispatch: invalid workspace export request")
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return ErrRouterClosed
	}
	u := r.workspaceExport
	if request.Step != "begin" {
		if u == nil || u.request.ID != env.ID || u.request.Assignment != env.Assignment {
			r.mu.Unlock()
			return r.sendWorkspaceExport(ctx, env, proto.WorkspaceExportResultPayload{Outcome: "rejected", ErrorCode: "resource_unavailable"})
		}
		if request.Step == "cancel" {
			u.cancel()
			r.mu.Unlock()
			return nil
		}
		select {
		case u.requests <- request:
			r.mu.Unlock()
			return nil
		default:
			u.cancel()
			r.mu.Unlock()
			return errors.New("dispatch: workspace export request already pending")
		}
	}
	_, code := r.workspaceResourceLocked(env.Assignment, proto.WorkspaceReadPayload{Handle: request.Handle, EnvironmentID: request.EnvironmentID})
	p := r.preparations[request.Handle]
	if code == "" && (u != nil || r.workspaceWrite != nil || !r.localWorkspace.CanExport() || p == nil || !p.workspaceReadOnly) {
		code = "resource_unavailable"
	}
	if code != "" {
		r.mu.Unlock()
		return r.sendWorkspaceExport(ctx, env, proto.WorkspaceExportResultPayload{Outcome: "rejected", ErrorCode: code})
	}
	owner, stop := r.shutdownContext(p.ctx)
	owner, cancel := context.WithTimeout(owner, 180*time.Second)
	u = &workspaceExport{request: env, requests: make(chan proto.WorkspaceExportPayload, 1), cancel: func() { cancel(); stop() }}
	u.requests <- request
	r.workspaceExport = u
	r.shutdownWG.Add(1)
	r.mu.Unlock()
	go r.runWorkspaceExport(owner, u)
	return nil
}

func (r *Router) runWorkspaceExport(ctx context.Context, u *workspaceExport) {
	defer r.shutdownWG.Done()
	reader, writer := io.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		err := r.localWorkspace.ExportOutputs(ctx, writer)
		_ = writer.CloseWithError(err)
	}()
	defer func() {
		u.cancel()
		_ = reader.Close()
		<-done
		r.mu.Lock()
		if r.workspaceExport == u {
			r.workspaceExport = nil
		}
		r.mu.Unlock()
	}()
	// Closing the reader unblocks a pending pipe read on cancellation or shutdown.
	stopRead := context.AfterFunc(ctx, func() { _ = reader.CloseWithError(ctx.Err()) })
	defer stopRead()
	var offset int64
	buffer := make([]byte, proto.WorkspaceExportChunkBytes)
	for {
		select {
		case request := <-u.requests:
			if request.Offset != offset {
				_ = r.sendWorkspaceExport(ctx, u.request, proto.WorkspaceExportResultPayload{Outcome: "failed", Offset: offset, ErrorCode: "invalid_request"})
				return
			}
		case <-ctx.Done():
			return
		}
		n, err := reader.Read(buffer)
		result := proto.WorkspaceExportResultPayload{Offset: offset}
		switch {
		case err == io.EOF:
			result.Outcome = "completed"
		case err != nil || n == 0 || int64(n) > proto.WorkspaceExportMaxBytes-offset:
			result.Outcome, result.ErrorCode = "failed", "export_failed"
		default:
			result.Outcome, result.Data = "chunk", buffer[:n]
			offset += int64(n)
		}
		if result.Outcome == "completed" {
			// The next owner may start immediately after receiving completion.
			<-done
			r.mu.Lock()
			if r.workspaceExport == u {
				r.workspaceExport = nil
			}
			r.mu.Unlock()
		}
		if r.sendWorkspaceExport(ctx, u.request, result) != nil || result.Outcome != "chunk" {
			return
		}
	}
}

func (r *Router) sendWorkspaceExport(ctx context.Context, request proto.Envelope, result proto.WorkspaceExportResultPayload) error {
	return r.reply(ctx, request, proto.TypeWorkspaceExportResult, result)
}
