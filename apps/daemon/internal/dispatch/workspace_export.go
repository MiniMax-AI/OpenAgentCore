package dispatch

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

// workspaceExport is one admitted export.
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
	environment, code := r.workspaceResourceLocked(env.Assignment, proto.WorkspaceReadPayload{Handle: request.Handle, EnvironmentID: request.EnvironmentID})
	p := r.preparations[request.Handle]
	if code == "" && (u != nil || r.workspaceWrite != nil || p.executor != nil) {
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
	done := r.trackWorkLocked(env.Assignment)
	r.shutdownWG.Add(1)
	r.mu.Unlock()
	go r.runWorkspaceExport(owner, u, environment, done)
	return nil
}

// runWorkspaceExport answers each request it admitted, even once the export is
// canceled, and runs done after its last result is sent.
func (r *Router) runWorkspaceExport(ctx context.Context, u *workspaceExport, environment Environment, done func()) {
	defer r.shutdownWG.Done()
	defer done()
	// A result has its own send budget, independent of the export's cancellation.
	send := func(result proto.WorkspaceExportResultPayload) error {
		return r.sendWorkspaceExport(context.WithoutCancel(ctx), u.request, result)
	}
	reader, writer := io.Pipe()
	exported := make(chan struct{})
	go func() {
		defer close(exported)
		err := environment.ExportOutputs(ctx, &exportWriter{output: writer})
		_ = writer.CloseWithError(err)
	}()
	var offset int64
	defer func() {
		u.cancel()
		_ = reader.Close()
		<-exported
		r.mu.Lock()
		if r.workspaceExport == u {
			r.workspaceExport = nil
		}
		r.mu.Unlock()
		select {
		case <-u.requests:
			_ = send(proto.WorkspaceExportResultPayload{Outcome: "failed", Offset: offset, ErrorCode: "export_failed"})
		default:
		}
	}()
	// Closing the reader unblocks a pending pipe read on cancellation or shutdown.
	stopRead := context.AfterFunc(ctx, func() { _ = reader.CloseWithError(ctx.Err()) })
	defer stopRead()
	buffer := make([]byte, proto.WorkspaceExportChunkBytes)
	for {
		select {
		case request := <-u.requests:
			if request.Offset != offset {
				_ = send(proto.WorkspaceExportResultPayload{Outcome: "failed", Offset: offset, ErrorCode: "invalid_request"})
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
			<-exported
			r.mu.Lock()
			if r.workspaceExport == u {
				r.workspaceExport = nil
			}
			r.mu.Unlock()
		}
		if send(result) != nil || result.Outcome != "chunk" {
			return
		}
	}
}

func (r *Router) sendWorkspaceExport(ctx context.Context, request proto.Envelope, result proto.WorkspaceExportResultPayload) error {
	return r.reply(ctx, request, proto.TypeWorkspaceExportResult, result)
}

// exportWriter bounds the archive and drops empty writes, which a pipe would
// deliver as empty reads.
type exportWriter struct {
	output io.Writer
	size   int64
}

func (w *exportWriter) Write(data []byte) (int, error) {
	if len(data) == 0 {
		return 0, nil
	}
	if int64(len(data)) > proto.WorkspaceExportMaxBytes-w.size {
		return 0, errors.New("workspace export exceeds bound")
	}
	n, err := w.output.Write(data)
	w.size += int64(n)
	return n, err
}
