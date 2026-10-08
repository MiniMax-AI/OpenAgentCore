package codex

import (
	"context"
	"fmt"
	"strings"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/localworkspace"
)

func prepareSessionPlan(ctx context.Context, req agent.PrepareRequest, cfg sessionConfig) (SessionPlan, error) {
	if err := validateNativeTransportEnvironment(); err != nil {
		return SessionPlan{}, err
	}
	mcpServers, mcpEnv, err := runtimeMCPServers(req)
	if err != nil {
		return SessionPlan{}, err
	}
	plan, err := BuildSessionPlan(req)
	if err != nil {
		return SessionPlan{}, fmt.Errorf("codex: build session plan: %w", err)
	}
	if err := configureSubagentObservations(&plan, req.PromptRequestPayload); err != nil {
		plan.Cleanup()
		return SessionPlan{}, err
	}
	disableProgrammaticTools(&plan, req.ExecutionControls)
	if req.LocalEnvironment != nil {
		plan.Cwd = req.WorkspaceRoot
	} else {
		// environment:none has no workspace; the Session's private home is its cwd.
		plan.Cwd = plan.home.View
	}

	if req.LocalEnvironment != nil {
		values, err := localworkspace.ReadOptionalToolEnvironment()
		if err != nil {
			plan.Cleanup()
			return SessionPlan{}, err
		}
		for key, value := range values {
			if strings.EqualFold(key, "CODEX_HOME") || strings.EqualFold(key, "HOME") || strings.EqualFold(key, "USERPROFILE") {
				continue
			}
			plan.Env = append(plan.Env, key+"="+value)
		}
	}

	if req.DisableSubagents {
		disableSubagents(&plan)
	}
	if mcpServers != nil {
		if err := configureMCP(&plan, mcpServers); err != nil {
			plan.Cleanup()
			return SessionPlan{}, err
		}
	}

	if req.ExecutionControls != nil {
		if err := prepareModelVerbosity(ctx, cfg.codexBinary, &plan); err != nil {
			plan.Cleanup()
			return SessionPlan{}, err
		}
	}

	plan.Env = append(plan.Env, mcpEnv...)

	return plan, nil
}
