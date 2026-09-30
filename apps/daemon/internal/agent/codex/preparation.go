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

// Prepare connects the native harness without creating a thread or starting model
// work. req supplies configuration and a stable state key, but no RunID or Prompt.
// owner owns the entire harness lifetime, including the eventual Session; it must
// not be a disposable readiness-request context. Initialization/readiness use their
// existing operation-local deadlines. Close an unused preparation explicitly.
func Prepare(owner context.Context, req proto.PromptRequestPayload) (*Prepared, error) {
	return newPreparation(owner, req, defaultSessionConfig())
}

func newSession(parent context.Context, req proto.PromptRequestPayload, out chan<- proto.Envelope, cfg sessionConfig) (*Session, error) {
	if out == nil {
		return nil, errors.New("codex: nil out channel")
	}
	runID, prompt := req.RunID, req.Input
	req.AgentStateKey = effectiveAgentStateKey(req)
	req.RunID, req.Input = "", nil
	prepared, err := newPreparation(parent, req, cfg)
	if err != nil {
		return nil, err
	}
	defer prepared.Close()
	return prepared.start(parent, runID, prompt, out)
}

func newPreparation(parent context.Context, req proto.PromptRequestPayload, cfg sessionConfig) (*Prepared, error) {
	if req.ExecutionControls != nil && req.ExecutionControls.OutputFormat != nil {
		return nil, errors.New("codex: structured output is not qualified")
	}
	if req.WorkspaceReadOnly {
		return nil, errors.New("codex: workspace reads use the local Runtime interface")
	}
	if req.RequireExistingNativeSession && (!req.StrictResume || req.AgentStateKey == "" || req.WorkspaceReadOnly) {
		return nil, errors.New("codex: native-session recovery requires strict private state")
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
	req.AgentStateKey = effectiveAgentStateKey(req)

	req.AgentOptions = executionOptions(req)
	plan, skillRoots, err := prepareSessionPlan(parent, req, cfg)
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
	for _, kv := range plan.ExtraConfig {
		rpcCfg.ExtraArgs = append(rpcCfg.ExtraArgs, "-c", kv[0]+"="+kv[1])
	}

	rpc := NewJSONRPCClient(rpcCfg)

	s := &Session{
		nativeHome:      nativeHomeFromPlan(plan),
		functions:       functions,
		observeMessages: req.ObserveMessages,

		observeToolObservations:   req.ObserveToolObservations,
		observeSubagentIdentities: req.ObserveSubagentIdentities && !req.DisableSubagents,
		cfg:                       cfg,
		rpc:                       rpc,
		cancelCtx:                 cancelCtx,
		cancelFn:                  cancelFn,
		waitDone:                  make(chan struct{}),
		cleanup:                   sync.OnceFunc(plan.Cleanup),
		bufs:                      NewItemBuffers(),
		resolvedModel:             plan.Model,
		interactions:              newPendingCodexInteractions(),
	}
	plan.Cleanup = s.cleanup
	p := &Prepared{
		session: s, plan: plan,
		resumeID: req.AgentSessionID, strictResume: req.StrictResume, requireExistingNativeSession: req.RequireExistingNativeSession,
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
