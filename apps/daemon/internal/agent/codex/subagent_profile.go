package codex

import (
	"context"
	"encoding/json"
	"errors"
	stdstrconv "strconv"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

func configureSubagentObservations(plan *SessionPlan, req proto.PromptRequestPayload) error {
	if !req.ObserveSubagentIdentities || req.DisableSubagents {
		return nil
	}
	plan.EnableFeatures = append(plan.EnableFeatures, "multi_agent")
	plan.ExtraConfig = append(plan.ExtraConfig, [2]string{"features.multi_agent", "true"})
	if req.MaxConcurrentSubagents != nil {
		if *req.MaxConcurrentSubagents < 1 {
			return errors.New("codex: invalid subagent concurrency limit")
		}
		plan.ExtraConfig = append(plan.ExtraConfig, [2]string{"agents.max_threads", stdstrconv.Itoa(*req.MaxConcurrentSubagents)})
	}
	return nil
}

// Code-mode collaboration calls and multi_agent_v2 leave no persisted receipt, so
// managed requirements that force them cannot be observed.
func verifySubagentObservationProfile(ctx context.Context, rpc *JSONRPCClient) error {
	raw, err := rpc.Request(ctx, "configRequirements/read", nil)
	var response struct {
		Requirements *struct {
			Features map[string]bool `json:"featureRequirements"`
		} `json:"requirements"`
	}
	if err != nil || json.Unmarshal(raw, &response) != nil {
		return errors.New("codex: subagent feature requirements unavailable")
	}
	if response.Requirements == nil {
		return nil
	}
	for _, feature := range []string{"code_mode", "code_mode_only", "code_mode_prewarm", "multi_agent_v2"} {
		if response.Requirements.Features[feature] {
			return errors.New("codex: required feature conflicts with subagent observation")
		}
	}
	return nil
}
