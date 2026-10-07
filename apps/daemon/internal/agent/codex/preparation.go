package codex

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	obslog "github.com/MiniMax-AI/OpenAgentCore/internal/obs/log"
)

func newPreparation(parent context.Context, req proto.PromptRequestPayload, cfg sessionConfig) (*Prepared, error) {
	if req.ExecutionControls != nil && req.ExecutionControls.OutputFormat != nil {
		return nil, errors.New("codex: structured output is not qualified")
	}
	if req.WorkspaceReadOnly {
		return nil, errors.New("codex: workspace reads use the local Runtime interface")
	}
	if req.RunID != "" || len(req.Input) != 0 {
		return nil, errors.New("codex: preparation does not accept a run identity or prompt")
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
	var plan SessionPlan
	var skillRoots []string
	if cfg.view != nil {
		plan, err = prepareViewPlan(parent, req, cfg)
	} else {
		plan, skillRoots, err = prepareSessionPlan(parent, req, cfg)
	}
	if err != nil {
		return nil, err
	}

	cancelCtx, cancelFn := context.WithCancel(parent)

	rpcCfg := JSONRPCConfig{
		Binary:          cfg.codexBinary,
		EnableFeatures:  plan.EnableFeatures,
		DisableFeatures: plan.DisableFeatures,
		Cwd:             plan.Cwd,
		Env:             append(os.Environ(), plan.Env...),
		LogTag:          "codex-preparation",
		Logger:          cfg.logger,
	}
	if cfg.view != nil {
		rpcCfg.Binary, rpcCfg.Env, rpcCfg.Launch = cfg.view.binary, plan.Env, cfg.view.Launch
	}
	for _, kv := range plan.ExtraConfig {
		rpcCfg.ExtraArgs = append(rpcCfg.ExtraArgs, "-c", kv[0]+"="+kv[1])
	}

	rpc := NewJSONRPCClient(rpcCfg)

	s := &Session{
		nativeHome:                plan.home,
		functions:                 functions,
		observeMessages:           req.ObserveMessages,
		observeSubagentIdentities: req.ObserveSubagentIdentities && !req.DisableSubagents,
		cfg:                       cfg,
		rpc:                       rpc,
		cancelCtx:                 cancelCtx,
		cancelFn:                  cancelFn,
		waitDone:                  make(chan struct{}),
		cleanup:                   sync.OnceFunc(plan.Cleanup),
		bufs:                      NewItemBuffers(),
		resolvedModel:             plan.Model,
	}
	plan.Cleanup = s.cleanup
	p := &Prepared{
		session: s, plan: plan,
		resumeID: req.AgentSessionID, requireExistingNativeSession: req.RequireExistingNativeSession,
		transferred: make(chan struct{}),
	}

	initParams := InitializeParams{
		ClientInfo:   InitializeClientInfo{Name: "oac-daemon", Version: "0.0.0"},
		Capabilities: &InitializeCapabilities{ExperimentalAPI: true},
	}
	if _, err := rpc.Start(cancelCtx, initParams); err != nil {
		return p.preparationFailed(fmt.Errorf("codex: rpc start: %w", err))
	}
	if req.ExecutionControls != nil && req.ExecutionControls.DisableProgrammaticToolCalling {
		if err := verifyProgrammaticToolsDisabled(cancelCtx, rpc); err != nil {
			return p.preparationFailed(err)
		}
	}
	if req.DisableExecutionEnvironment {
		if err := verifyNoExecutionEnvironment(cancelCtx, rpc); err != nil {
			return p.preparationFailed(err)
		}
	}
	if s.observeSubagentIdentities {
		if err := verifySubagentObservationProfile(cancelCtx, rpc, plan.Cwd); err != nil {
			return p.preparationFailed(err)
		}
	}

	if plan.mcpServers != nil {
		if err := verifyMCPConfig(cancelCtx, rpc, plan); err != nil {
			return p.preparationFailed(err)
		}
	}
	if len(skillRoots) > 0 {
		if err := setSkillExtraRoots(cancelCtx, rpc, skillRoots); err != nil {
			return p.preparationFailed(fmt.Errorf("codex: register skill root: %w", err))
		}
	}

	go p.watchOwner()
	return p, nil
}

func (p *Prepared) preparationFailed(cause error) (*Prepared, error) {
	if err := p.Close(); err != nil {
		return p, errors.Join(cause, err)
	}
	return nil, cause
}
