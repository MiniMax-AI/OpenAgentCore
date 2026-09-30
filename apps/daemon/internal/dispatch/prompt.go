package dispatch

import (
	"context"
	"errors"
	"fmt"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	obslog "github.com/MiniMax-AI/OpenAgentCore/internal/obs/log"
)

func (r *Router) handlePromptRequest(callerCtx context.Context, env proto.Envelope) error {
	r.log.InfoContext(callerCtx, "handlePromptRequest: decoding payload", "env_id", env.ID, "env_type", env.Type)
	var req proto.PromptRequestPayload
	if err := env.DecodePayload(&req); err != nil {
		r.log.ErrorContext(callerCtx, "handlePromptRequest: decode failed", "env_id", env.ID, "err", err)
		return fmt.Errorf("dispatch: decode prompt_request: %w", err)
	}
	// Envelope.ID is the run id; payload mirrors it but envelope wins.
	runID := env.ID
	if runID == "" {
		runID = req.RunID
	}
	if runID == "" {
		r.log.ErrorContext(callerCtx, "handlePromptRequest: missing run id")
		return errors.New("dispatch: prompt_request missing run id (Envelope.ID and Payload.RunID both empty)")
	}
	req.RunID = runID
	var err error
	if req, err = r.localWorkspace.Configure(req); err != nil {
		r.emitTerminalError(callerCtx, runID, err.Error())
		return err
	}
	if req.AgentKind == "" {
		r.log.ErrorContext(callerCtx, "handlePromptRequest: missing agent_kind", "run_id", runID)
		return errors.New("dispatch: prompt_request missing agent_kind")
	}
	if req.WorkspaceReadOnly {
		err := errors.New("read-only preparation cannot accept a prompt")
		r.emitTerminalError(callerCtx, runID, err.Error())
		return err
	}
	caps, available := r.availableCapabilities(req.AgentKind)
	if !available {
		_, err := r.registry.Resolve(req.AgentKind)
		if err == nil {
			err = errors.New("engine is unavailable on this runtime")
		}
		r.emitTerminalError(callerCtx, runID, err.Error())
		return err
	}
	if len(req.FunctionTools) > 0 && !caps.FunctionTools.IsSupported() {
		err := errors.New("engine does not support function tools")
		r.emitTerminalError(callerCtx, runID, err.Error())
		return err
	}
	if err := validateExecutionEnvironment(req, caps); err != nil {
		r.emitTerminalError(callerCtx, runID, err.Error())
		return err
	}
	r.log.InfoContext(callerCtx, "handlePromptRequest: decoded",
		"run_id", runID, "agent_kind", req.AgentKind,
		"message_count", len(req.Input),
		"has_agent_options", req.AgentOptions != nil,
		"agent_session_id", req.AgentSessionID,
		"agent_state_key", req.AgentStateKey)

	factory, err := r.registry.Resolve(req.AgentKind)
	if err != nil {
		r.log.ErrorContext(callerCtx, "handlePromptRequest: registry.Resolve failed", "run_id", runID, "agent_kind", req.AgentKind, "err", err)
		// Synthesize error+done so the server-side stream closes
		// cleanly instead of waiting for a done that never comes.
		r.emitTerminalError(callerCtx, runID, fmt.Sprintf("unsupported agent_kind %q on this daemon", req.AgentKind))
		return err
	}
	r.log.InfoContext(callerCtx, "handlePromptRequest: factory resolved", "run_id", runID, "agent_kind", req.AgentKind)

	// Lock-protect duplicate-run check + insert so two prompt_requests
	// with the same RunID can't both start sessions.
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		r.log.ErrorContext(callerCtx, "handlePromptRequest: router closed", "run_id", runID)
		return ErrRouterClosed
	}
	if r.runtimePreparation != nil || r.workspaceWrite != nil || r.workspaceExport != nil {
		r.mu.Unlock()
		err := errors.New("local workspace has an unsettled write")
		r.emitTerminalError(callerCtx, runID, err.Error())
		return err
	}
	if _, dup := r.sessions[runID]; dup {
		r.mu.Unlock()
		r.log.WarnContext(callerCtx, "ignoring duplicate prompt_request", "run_id", runID)
		return nil
	}
	stateKey := sessionStateKey(req)
	r.touchIdleLocked(stateKey)
	sessionCtx, sessionCancel := context.WithCancel(context.Background())
	// Re-attach the inbound trace so every log under this run shows
	// the same trace_id as the prompt_request that started it.
	if carrier, ok := obslog.TraceFromContext(callerCtx); ok {
		sessionCtx = obslog.WithTrace(sessionCtx, carrier)
	}
	out := make(chan proto.Envelope, 64)
	state := &sessionState{
		runID:               runID,
		capabilities:        caps,
		environmentID:       req.EnvironmentID(),
		stateKey:            stateKey,
		out:                 out,
		ctx:                 sessionCtx,
		ctxCancel:           sessionCancel,
		pendingIDs:          make(map[string]struct{}),
		pendingAsks:         make(map[string]struct{}),
		traceparent:         env.Trace,
		retain:              stateKey != "",
		releaseOnCompletion: req.ReleaseOnCompletion,
	}
	r.sessions[runID] = state
	r.mu.Unlock()

	r.log.InfoContext(callerCtx, "handlePromptRequest: calling factory", "run_id", runID)
	req, err = r.localWorkspace.Prepare(sessionCtx, req)
	var sess agent.Session
	if err == nil {
		sess, err = factory(sessionCtx, req, out)
	}
	if err != nil {
		r.log.ErrorContext(callerCtx, "handlePromptRequest: factory call failed", "run_id", runID, "agent_kind", req.AgentKind, "err", err)
		// Roll back the registration, cancel ctx, surface error+done
		// so the server doesn't hang on a phantom run.
		r.mu.Lock()
		delete(r.sessions, runID)
		r.mu.Unlock()
		sessionCancel()
		r.emitTerminalError(callerCtx, runID, fmt.Sprintf("agent factory failed: %v", err))
		return fmt.Errorf("dispatch: factory %q: %w", req.AgentKind, err)
	}
	r.log.InfoContext(callerCtx, "handlePromptRequest: session created, starting pump", "run_id", runID)
	r.mu.Lock()
	state.session = sess
	r.mu.Unlock()

	r.shutdownWG.Add(1)
	go r.pump(state)
	return nil
}
