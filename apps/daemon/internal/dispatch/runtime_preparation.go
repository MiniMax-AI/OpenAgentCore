package dispatch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/localworkspace"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentcapabilities"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/google/uuid"
)

const runtimePreparationTimeout = 120 * time.Second

// Router.mu protects one connection-local transfer. Partial installation data
// belongs to the bound Environment and is never removed by transfer cleanup.
type runtimePreparationTransfer struct {
	envelope  proto.Envelope
	request   proto.RuntimePreparePayload
	data      []byte
	ready     chan struct{}
	cancel    context.CancelFunc
	finished  bool
	apply     bool
	uncertain bool
}

func (r *Router) handleRuntimePrepare(ctx context.Context, env proto.Envelope) error {
	id, err := uuid.Parse(env.ID)
	if err != nil || id == uuid.Nil || id.String() != env.ID {
		return errors.New("dispatch: invalid Runtime preparation identity")
	}
	var request proto.RuntimePreparePayload
	if len(env.Payload) > proto.RuntimePrepareMaxFrameBytes || env.DecodePayload(&request) != nil || !proto.ValidRuntimePrepareRequest(request) {
		r.mu.Lock()
		pending := r.runtimePreparation != nil && r.runtimePreparation.envelope.ID == env.ID
		if pending && !r.runtimePreparation.finished {
			r.finishRuntimePreparationTransferLocked(r.runtimePreparation, false)
		}
		r.mu.Unlock()
		if pending {
			// A late malformed frame cannot report rejection of an earlier commit.
			return errors.New("dispatch: malformed pending Runtime preparation frame")
		}
		return r.sendRuntimePrepareResult(ctx, env.ID, rejectedRuntimePreparation("invalid_request"))
	}
	r.mu.Lock()
	if r.closed || r.suspension != nil {
		r.mu.Unlock()
		return ErrRouterClosed
	}
	if request.Step == "begin" {
		if r.runtimePreparation != nil {
			duplicate := r.runtimePreparation.envelope.ID == env.ID
			r.mu.Unlock()
			if duplicate {
				return errors.New("dispatch: Runtime preparation already admitted")
			}
			return r.sendRuntimePrepareResult(ctx, env.ID, rejectedRuntimePreparation("runtime_preparation_capacity"))
		}
		if r.localWorkspace == nil || !r.localWorkspace.Matches(request.EnvironmentID, request.SessionID) || r.runtimePreparationResourcesBusyLocked() {
			r.mu.Unlock()
			return r.sendRuntimePrepareResult(ctx, env.ID, rejectedRuntimePreparation("resource_unavailable"))
		}
		owner, cancel := context.WithTimeout(context.WithoutCancel(ctx), runtimePreparationTimeout)
		u := &runtimePreparationTransfer{
			envelope: env, request: request, data: make([]byte, 0, request.SizeBytes),
			ready: make(chan struct{}), cancel: cancel,
		}
		r.runtimePreparation = u
		r.shutdownWG.Add(1)
		r.mu.Unlock()
		go r.runRuntimePreparationTransfer(owner, u, r.localWorkspace.ApplyRuntimePreparation)
		if err := r.sendRuntimePrepareResult(ctx, env.ID, proto.RuntimePrepareResultPayload{Outcome: "ready"}); err != nil {
			cancel()
			return err
		}
		return nil
	}
	u := r.runtimePreparation
	if u == nil || u.envelope.ID != env.ID {
		r.mu.Unlock()
		return r.sendRuntimePrepareResult(ctx, env.ID, rejectedRuntimePreparation("resource_unavailable"))
	}
	if u.finished {
		r.mu.Unlock()
		return errors.New("dispatch: Runtime preparation body already closed")
	}
	if request.Step == "chunk" && request.Offset == len(u.data) && len(request.Data) <= u.request.SizeBytes-len(u.data) {
		u.data = append(u.data, request.Data...)
		offset := len(u.data)
		r.mu.Unlock()
		if err := r.sendRuntimePrepareResult(ctx, env.ID, proto.RuntimePrepareResultPayload{Outcome: "received", Offset: offset}); err != nil {
			u.cancel()
			return err
		}
		return nil
	}
	apply := false
	if request.Step == "commit" && len(u.data) == u.request.SizeBytes {
		if u.request.Action == "finalize" || u.request.Action == "initialize" {
			apply = true
		} else {
			digest := sha256.Sum256(u.data)
			apply = hex.EncodeToString(digest[:]) == u.request.SHA256
		}
	}
	r.finishRuntimePreparationTransferLocked(u, apply)
	r.mu.Unlock()
	return nil
}

func (r *Router) runtimePreparationResourcesBusyLocked() bool {
	if r.workspaceWrite != nil || r.workspaceExport != nil || len(r.workspaceReads) != 0 || len(r.sessions) != 0 || len(r.idle) != 0 || len(r.executors) != 0 {
		return true
	}
	for _, p := range r.preparations {
		if p.owns || p.busy {
			return true
		}
	}
	return false
}

func (r *Router) finishRuntimePreparationTransferLocked(u *runtimePreparationTransfer, apply bool) {
	u.apply, u.finished = apply, true
	close(u.ready)
}

// apply must return only after its local mutations stop. Cancellation requests
// shutdown, but cannot release ownership while that call is still running.
func (r *Router) runRuntimePreparationTransfer(ctx context.Context, u *runtimePreparationTransfer, apply func(context.Context, proto.RuntimePreparePayload, []byte) error) {
	defer r.shutdownWG.Done()
	defer u.cancel()
	select {
	case <-u.ready:
	case <-r.shutdownCh:
	case <-ctx.Done():
	}
	r.mu.Lock()
	admitted := u.apply && !r.closed && ctx.Err() == nil
	u.finished = true
	data := u.data
	u.data = nil
	r.mu.Unlock()
	result := rejectedRuntimePreparation("invalid_request")
	if admitted {
		result = runtimePreparationResult(apply(ctx, u.request, data), u.request.SizeBytes)
	}
	// Release the potentially large body before waiting on transport delivery.
	data = nil
	r.mu.Lock()
	u.uncertain = result.Outcome == "unknown"
	if !u.uncertain && r.runtimePreparation == u {
		r.runtimePreparation = nil
	}
	r.mu.Unlock()
	// The result has a separate send budget, independent of an installation timeout.
	_ = r.sendRuntimePrepareResult(context.WithoutCancel(ctx), u.envelope.ID, result)
}

func rejectedRuntimePreparation(code string) proto.RuntimePrepareResultPayload {
	return proto.RuntimePrepareResultPayload{Outcome: "rejected", ErrorCode: code}
}

func runtimePreparationResult(err error, size int) proto.RuntimePrepareResultPayload {
	if err == nil {
		return proto.RuntimePrepareResultPayload{Outcome: "completed", SizeBytes: size}
	}
	var initialization *localworkspace.InitializationFailure
	if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) && errors.As(err, &initialization) {
		code := 0
		if initialization.ExitCode != nil {
			code = *initialization.ExitCode
		}
		if (initialization.ExitCode == nil || code > 0) && code <= 255 {
			return proto.RuntimePrepareResultPayload{Outcome: "failed", ErrorCode: "runtime_preparation_failed", ExitCode: code}
		}
	}
	if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) && errors.Is(err, agentcapabilities.ErrInvalid) {
		return proto.RuntimePrepareResultPayload{Outcome: "failed", ErrorCode: "runtime_preparation_failed"}
	}
	return proto.RuntimePrepareResultPayload{Outcome: "unknown", ErrorCode: "runtime_preparation_unconfirmed"}
}

func (r *Router) sendRuntimePrepareResult(ctx context.Context, id string, result proto.RuntimePrepareResultPayload) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	env, err := proto.NewEnvelope(proto.TypeRuntimePrepareResult, id, result)
	if err != nil {
		return err
	}
	return r.sender.Send(ctx, env)
}
