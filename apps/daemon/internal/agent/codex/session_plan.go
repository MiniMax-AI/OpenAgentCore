package codex

import (
	"context"
	"fmt"
	"strings"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/localworkspace"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

func prepareSessionPlan(ctx context.Context, req proto.PromptRequestPayload, cfg sessionConfig) (SessionPlan, []string, error) {
	if err := validateNativeTransportEnvironment(req); err != nil {
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
	plan, err := BuildSessionPlan(req.RunID, req.AgentStateKey, req.AgentOptions)
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

	if stringOpt(req.AgentOptions, "model_verbosity") != "" {
		if err := prepareModelVerbosity(ctx, cfg.codexBinary, &plan); err != nil {
			plan.Cleanup()
			return SessionPlan{}, nil, err
		}
	}

	if req.DisableExecutionEnvironment {
		plan.Env = append(plan.Env, "CODEX_EXEC_SERVER_URL=none")
	}
	var skillRoots []string
	if req.LocalEnvironment != nil && len(req.LocalEnvironment.Skills) > 0 {
		err = verifyHostedSkills(req.LocalEnvironment.Skills)
		if err == nil {
			for _, skill := range req.LocalEnvironment.Skills {
				skillRoots = append(skillRoots, localworkspace.SkillPath(skill))
			}
		}
	} else if !req.DisableExecutionEnvironment {
		var root string
		root, err = prepareManagedSkills(ctx, cfg.logger, req)
		if root != "" {
			skillRoots = []string{root}
		}
	}
	if err != nil {
		plan.Cleanup()
		return SessionPlan{}, nil, err
	}

	plan.Env = append(plan.Env, mcpEnv...)

	return plan, skillRoots, nil
}
