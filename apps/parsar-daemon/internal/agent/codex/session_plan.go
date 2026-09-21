package codex

import (
	"context"
	"fmt"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/localworkspace"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func prepareSessionPlan(ctx context.Context, req proto.PromptRequestPayload, cfg sessionConfig) (SessionPlan, []string, error) {
	if err := validateNativeTransportEnvironment(req); err != nil {
		return SessionPlan{}, nil, err
	}
	profile, err := managedPermissionProfile(req, cfg)
	if err != nil {
		return SessionPlan{}, nil, err
	}
	mcpServers, err := publicMCPHTTPServers(req)
	if err != nil {
		return SessionPlan{}, nil, err
	}
	plan, err := BuildSessionPlan(req.RunID, req.AgentStateKey, req.WorkDir, req.AgentOptions)
	if err != nil {
		return SessionPlan{}, nil, fmt.Errorf("codex: build session plan: %w", err)
	}
	if err := configureSubagentObservations(&plan, req); err != nil {
		plan.Cleanup()
		return SessionPlan{}, nil, err
	}
	if profile != "" {
		plan.Sandbox = ""
		plan.Permissions = profile
		plan.ExtraConfig = append(plan.ExtraConfig, [2]string{"default_permissions", tomlQuoteString(profile)})
		configureRestrictedShellEnvironment(&plan)
		// Native login-shell snapshots live outside the managed tool filesystem.
		plan.ExtraConfig = append(plan.ExtraConfig, [2]string{"features.shell_snapshot", "false"})
	}

	if req.LocalEnvironment != nil && req.LocalEnvironment.ToolEnvironment {
		if err := localworkspace.VerifyToolEnvironment(req.LocalEnvironment.SystemPackages); err != nil {
			plan.Cleanup()
			return SessionPlan{}, nil, err
		}
		plan.Env = append(plan.Env, "PARSAR_RUNTIME_TOOL_ENV=1")
		if req.LocalEnvironment.SystemPackages {
			if err := prepareSystemToolAnchor(); err != nil {
				plan.Cleanup()
				return SessionPlan{}, nil, err
			}
			plan.Env = append(plan.Env, "PARSAR_RUNTIME_SYSTEM_PACKAGES=1")
		}
		plan.ExtraConfig = append(plan.ExtraConfig, [2]string{"features.hooks", "true"})
	}

	if req.DisableSubagents {
		disableSubagents(&plan)
	}
	mcpBearerEnv := prepareMCPHTTPBearer(mcpServers, req.MCPHTTPServers)
	mcpServers, environmentMCPEnv, err := mergeEnvironmentMCP(mcpServers, req.LocalEnvironment)
	if err != nil {
		plan.Cleanup()
		return SessionPlan{}, nil, err
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

	plan.Env = append(plan.Env, mcpBearerEnv...)
	plan.Env = append(plan.Env, environmentMCPEnv...)
	if cfg.runtimeNetwork.Access == "restricted" {
		if err := prepareManagedNetwork(&plan, cfg.runtimeNetwork); err != nil {
			plan.Cleanup()
			return SessionPlan{}, nil, err
		}
	}
	return plan, skillRoots, nil
}
