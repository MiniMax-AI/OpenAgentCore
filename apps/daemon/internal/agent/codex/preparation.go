package codex

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	obslog "github.com/MiniMax-AI/OpenAgentCore/internal/obs/log"
)

func newExecutor(parent context.Context, req agent.PrepareRequest, cfg sessionConfig) (*Executor, error) {
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
	if _, err := rpc.Start(cancelCtx, initParams); err != nil {
		return e.preparationFailed(fmt.Errorf("codex: rpc start: %w", err))
	}
	if req.ExecutionControls != nil && req.ExecutionControls.DisableProgrammaticToolCalling {
		if err := verifyProgrammaticToolsDisabled(cancelCtx, rpc); err != nil {
			return e.preparationFailed(err)
		}
	}
	if req.DisableExecutionEnvironment {
		if err := verifyNoExecutionEnvironment(cancelCtx, rpc); err != nil {
			return e.preparationFailed(err)
		}
	}
	if s.observeSubagentIdentities {
		if err := verifySubagentObservationProfile(cancelCtx, rpc, plan.Cwd); err != nil {
			return e.preparationFailed(err)
		}
	}

	if plan.mcpServers != nil {
		if err := verifyMCPConfig(cancelCtx, rpc, plan); err != nil {
			return e.preparationFailed(err)
		}
	}
	if len(req.Skills) > 0 {
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
