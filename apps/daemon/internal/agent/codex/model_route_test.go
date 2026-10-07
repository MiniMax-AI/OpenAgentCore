package codex

import (
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/modelprovider"
)

func TestPlanRejectsNonNativeFrozenProvider(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for _, protocol := range []modelprovider.Protocol{modelprovider.Anthropic, modelprovider.ChatCompletions} {
		t.Run(string(protocol), func(t *testing.T) {
			provider := &modelprovider.Provider{Protocol: protocol, BaseURL: "https://model.invalid", APIKey: "private-sentinel"}
			plan, err := BuildSessionPlan(proto.PromptRequestPayload{RunID: "recovered", AgentStateKey: "frozen-state", Model: "frozen-model", ModelProvider: provider})
			if plan.Cleanup != nil {
				plan.Cleanup()
			}
			if err == nil || !strings.Contains(err.Error(), "does not support") || strings.Contains(err.Error(), "private-sentinel") {
				t.Fatalf("non-native snapshot accepted: %v", err)
			}
		})
	}
}

func TestPlanRejectsProviderWithoutModel(t *testing.T) {
	provider := &modelprovider.Provider{Protocol: modelprovider.Responses, BaseURL: "https://model.example/v1", APIKey: "fixture"}
	if _, err := BuildSessionPlan(proto.PromptRequestPayload{AgentStateKey: "state", ModelProvider: provider}); err == nil {
		t.Fatal("a provider without a model was accepted")
	}
}
