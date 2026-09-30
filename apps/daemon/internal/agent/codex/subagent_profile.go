package codex

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	stdstrconv "strconv"
	"strings"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

func configureSubagentObservations(plan *SessionPlan, req proto.PromptRequestPayload) error {
	if !req.ObserveSubagentIdentities || req.DisableSubagents {
		return nil
	}
	for _, feature := range []string{"hooks", "plugins", "code_mode", "code_mode_only", "code_mode_prewarm", "multi_agent_v2"} {
		plan.EnableFeatures = slices.DeleteFunc(plan.EnableFeatures, func(value string) bool { return value == feature })
		if !slices.Contains(plan.DisableFeatures, feature) {
			plan.DisableFeatures = append(plan.DisableFeatures, feature)
		}
		plan.ExtraConfig = append(plan.ExtraConfig, [2]string{"features." + feature, "false"})
	}
	plan.DisableFeatures = slices.DeleteFunc(plan.DisableFeatures, func(value string) bool { return value == "multi_agent" })
	if !slices.Contains(plan.EnableFeatures, "multi_agent") {
		plan.EnableFeatures = append(plan.EnableFeatures, "multi_agent")
	}
	plan.ExtraConfig = append(plan.ExtraConfig, [2]string{"features.multi_agent", "true"}, [2]string{"agents.max_depth", "64"})
	if req.MaxConcurrentSubagents != nil {
		if *req.MaxConcurrentSubagents < 1 {
			return errors.New("codex: invalid subagent concurrency limit")
		}
		plan.ExtraConfig = append(plan.ExtraConfig, [2]string{"agents.max_threads", stdstrconv.Itoa(*req.MaxConcurrentSubagents)})
	}
	return nil
}

func verifySubagentObservationProfile(ctx context.Context, rpc *JSONRPCClient, cwd string) error {
	hooks, err := readNativeToolHooks(ctx, rpc, cwd)
	if err != nil {
		return err
	}
	if len(hooks) != 0 {
		return errors.New("codex: subagent observation requires hooks disabled")
	}

	raw, err := rpc.Request(ctx, "configRequirements/read", nil)
	var response struct {
		Requirements *struct {
			AllowManagedHooksOnly bool            `json:"allowManagedHooksOnly"`
			Features              map[string]bool `json:"featureRequirements"`
		} `json:"requirements"`
	}
	if err != nil || json.Unmarshal(raw, &response) != nil {
		return errors.New("codex: subagent hook requirements unavailable")
	}
	if response.Requirements == nil {
		if len(hooks) != 0 {
			return errors.New("codex: managed-only subagent hooks are not enforced")
		}
		return nil
	}
	requirements := response.Requirements
	if (len(hooks) != 0 || requirements.Features["hooks"]) && !requirements.AllowManagedHooksOnly {
		return errors.New("codex: managed-only subagent hooks are not enforced")
	}
	for _, feature := range []string{"plugins", "code_mode", "code_mode_only", "code_mode_prewarm", "multi_agent_v2"} {
		if requirements.Features[feature] {
			return errors.New("codex: required feature conflicts with subagent observation profile")
		}
	}
	return nil
}

func nativeHomeFromPlan(plan SessionPlan) string {
	for _, value := range plan.Env {
		if strings.HasPrefix(value, "CODEX_HOME=") {
			return strings.TrimPrefix(value, "CODEX_HOME=")
		}
	}
	return ""
}
