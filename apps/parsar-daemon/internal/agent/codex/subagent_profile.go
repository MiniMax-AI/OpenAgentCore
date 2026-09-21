package codex

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	stdstrconv "strconv"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func configureSubagentObservations(plan *SessionPlan, req proto.PromptRequestPayload) error {
	if !req.ObserveSubagentIdentities || req.DisableSubagents {
		return nil
	}
	if req.LocalEnvironment != nil && req.LocalEnvironment.ToolEnvironment {
		return errors.New("codex: multi_agent with initialized tool environment hooks is not supported")
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
	raw, err := rpc.Request(ctx, "hooks/list", map[string]any{"cwds": []string{cwd}})
	var result struct {
		Data []struct {
			Cwd    string            `json:"cwd"`
			Hooks  []json.RawMessage `json:"hooks"`
			Errors []json.RawMessage `json:"errors"`
		} `json:"data"`
	}
	if err != nil || json.Unmarshal(raw, &result) != nil || len(result.Data) != 1 || result.Data[0].Cwd != cwd || len(result.Data[0].Hooks) != 0 || len(result.Data[0].Errors) != 0 {
		return errors.New("codex: subagent observation requires hooks disabled")
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
