package codex

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	obslog "github.com/MiniMax-AI/OpenAgentCore/internal/obs/log"
)

func newExecutor(parent context.Context, req agent.PrepareRequest, cfg sessionConfig) (_ *Executor, err error) {
	if req.WorkspaceReadOnly {
		return nil, errors.New("codex: workspace reads use the local Runtime interface")
	}
	if cfg.logger == nil {
		cfg.logger = obslog.Bg()
	}
	if cfg.codexBinary == "" {
		cfg.codexBinary = defaultBinary()
	}
	if cfg.killTimeout <= 0 {
		cfg.killTimeout = rpcKillTimeout
	}
	functions, err := prepareFunctionTools(req.FunctionTools)
	if err != nil {
		return nil, err
	}
	started := time.Now()
	phase := "view_plan"
	defer func() {
		if err == nil {
			return
		}
		// Native errors may contain credentials. Record only the failed phase
		// and owner context; preparation cleanup cancels its child context.
		contextOutcome := "active"
		switch {
		case errors.Is(parent.Err(), context.DeadlineExceeded):
			contextOutcome = "deadline_exceeded"
		case errors.Is(parent.Err(), context.Canceled):
			contextOutcome = "cancelled"
		}
		cfg.logger.Info("codex preparation failed", "session_id", req.Assignment.SessionID, "phase", phase, "duration_ms", time.Since(started).Milliseconds(), "context_outcome", contextOutcome)
	}()
	plan, err := prepareViewPlan(parent, req, cfg)
	if err != nil {
		return nil, err
	}

	cancelCtx, cancelFn := context.WithCancel(parent)

	rpcCfg := JSONRPCConfig{
		Binary:          cfg.view.binary,
		EnableFeatures:  plan.EnableFeatures,
		DisableFeatures: plan.DisableFeatures,
		Cwd:             plan.Cwd,
		Env:             plan.Env,
		Launch:          cfg.view.Launch,
		LogTag:          "codex-preparation",
		Logger:          cfg.logger,
	}
	for _, kv := range plan.ExtraConfig {
		rpcCfg.ExtraArgs = append(rpcCfg.ExtraArgs, "-c", kv[0]+"="+kv[1])
	}

	rpc := NewJSONRPCClient(rpcCfg)

	s := &Session{
		nativeHome:                plan.home,
		functions:                 functions,
		observeSubagentIdentities: req.ObserveSubagentIdentities && !req.DisableSubagents,
		cfg:                       cfg,
		rpc:                       rpc,
		cancelCtx:                 cancelCtx,
		cancelFn:                  cancelFn,
		resolvedModel:             plan.Model,
	}
	plan.Cleanup = sync.OnceFunc(plan.Cleanup)
	e := &Executor{base: s, plan: plan, resumeID: req.AgentSessionID, requireExistingNativeSession: req.RequireExistingNativeSession}

	initParams := InitializeParams{
		ClientInfo:   InitializeClientInfo{Name: "oac-daemon", Version: "0.0.0"},
		Capabilities: &InitializeCapabilities{ExperimentalAPI: true},
	}
	phase = "initialize"
	if _, err := rpc.Start(cancelCtx, initParams); err != nil {
		return e.preparationFailed(fmt.Errorf("codex: rpc start: %w", err))
	}
	if req.ExecutionControls != nil && req.ExecutionControls.DisableProgrammaticToolCalling {
		phase = "programmatic_tools_configuration"
		if err := verifyProgrammaticToolsDisabled(cancelCtx, rpc); err != nil {
			return e.preparationFailed(err)
		}
	}
	if req.DisableExecutionEnvironment {
		phase = "execution_environment_configuration"
		if err := verifyNoExecutionEnvironment(cancelCtx, rpc); err != nil {
			return e.preparationFailed(err)
		}
	}
	if s.observeSubagentIdentities {
		phase = "subagent_observation_configuration"
		if err := verifySubagentObservationProfile(cancelCtx, rpc, plan.Cwd); err != nil {
			return e.preparationFailed(err)
		}
	}

	if plan.mcpServers != nil {
		phase = "mcp_configuration"
		if err := verifyMCPConfig(cancelCtx, rpc, plan); err != nil {
			return e.preparationFailed(err)
		}
	}
	if len(req.Skills) > 0 {
		phase = "skill_registration"
		if err := registerSkills(cancelCtx, rpc, plan.Cwd, req.Skills); err != nil {
			return e.preparationFailed(err)
		}
	}

	return e, nil
}

// preparationFailed releases the unused process and plan. An unconfirmed release
// keeps them in the returned Executor for a later Close.
func (e *Executor) preparationFailed(cause error) (*Executor, error) {
	e.base.cancelFn()
	if err := e.base.rpc.Close(); err != nil {
		return e, errors.Join(cause, err)
	}
	e.plan.Cleanup()
	return nil, cause
}
