package codex

import (
	"context"
	"fmt"
	"strings"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/localworkspace"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

func prepareSessionPlan(ctx context.Context, req proto.PromptRequestPayload, cfg sessionConfig) (SessionPlan, []string, error) {
	if err := validateNativeTransportEnvironment(); err != nil {
		return SessionPlan{}, nil, err
	}
	_, err := runtimePermissionProfile(req)
	if err != nil {
		return SessionPlan{}, nil, err
	}
	mcpServers, mcpEnv, err := runtimeMCPServers(req)
	if err != nil {
		return SessionPlan{}, nil, err
	}
	plan, err := BuildSessionPlan(req)
	if err != nil {
		return SessionPlan{}, nil, fmt.Errorf("codex: build session plan: %w", err)
	}
	if err := configureSubagentObservations(&plan, req); err != nil {
		plan.Cleanup()
		return SessionPlan{}, nil, err
	}
	disableProgrammaticTools(&plan, req.ExecutionControls)
	if req.LocalEnvironment != nil {
		plan.Cwd = req.LocalEnvironment.WorkspaceRoot
		plan.Sandbox = "danger-full-access"
		plan.Permissions = ""
		plan.ApprovalPolicy = AskForApproval{String: "never"}
	} else if req.DisableExecutionEnvironment {
		// environment:none has no workspace; the Session's private home is its cwd.
		plan.Cwd = plan.home.View
	}

	if req.LocalEnvironment != nil {
		values, err := localworkspace.ReadOptionalToolEnvironment()
		if err != nil {
			plan.Cleanup()
			return SessionPlan{}, nil, err
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
			return SessionPlan{}, nil, err
		}
	}

	if req.ExecutionControls != nil {
		if err := prepareModelVerbosity(ctx, cfg.codexBinary, &plan); err != nil {
			plan.Cleanup()
			return SessionPlan{}, nil, err
		}
	}

	var skillRoots []string
	if req.LocalEnvironment != nil && len(req.LocalEnvironment.Skills) > 0 {
		if err := verifyHostedSkills(req.LocalEnvironment.Skills); err != nil {
			plan.Cleanup()
			return SessionPlan{}, nil, err
		}
		for _, skill := range req.LocalEnvironment.Skills {
			skillRoots = append(skillRoots, localworkspace.SkillPath(skill))
		}
	}

	plan.Env = append(plan.Env, mcpEnv...)

	return plan, skillRoots, nil
}
