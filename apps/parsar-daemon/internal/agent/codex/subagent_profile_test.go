package codex

import (
	"slices"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func TestSubagentProfileOverridesUnsafeNativeFeatures(t *testing.T) {
	limit := 6
	plan := SessionPlan{EnableFeatures: []string{"hooks", "code_mode", "plugins", "multi_agent_v2"}, DisableFeatures: []string{"multi_agent"}}
	if err := configureSubagentObservations(&plan, proto.PromptRequestPayload{ObserveSubagentIdentities: true, MaxConcurrentSubagents: &limit}); err != nil {
		t.Fatal(err)
	}
	for _, feature := range []string{"hooks", "plugins", "code_mode", "code_mode_only", "code_mode_prewarm", "multi_agent_v2"} {
		if slices.Contains(plan.EnableFeatures, feature) || !slices.Contains(plan.DisableFeatures, feature) {
			t.Fatal(feature, plan)
		}
	}
	if !slices.Contains(plan.ExtraConfig, [2]string{"agents.max_threads", "6"}) || !slices.Contains(plan.ExtraConfig, [2]string{"agents.max_depth", "64"}) {
		t.Fatal(plan.ExtraConfig)
	}
}

func TestSubagentProfileDoesNotDisableRequiredToolEnvironment(t *testing.T) {
	request := proto.PromptRequestPayload{ObserveSubagentIdentities: true, LocalEnvironment: &proto.LocalEnvironment{ToolEnvironment: true}}
	if err := configureSubagentObservations(&SessionPlan{}, request); err == nil {
		t.Fatal("incompatible hook profile accepted")
	}
	request.DisableSubagents = true
	if err := configureSubagentObservations(&SessionPlan{}, request); err != nil {
		t.Fatal("single-agent tool environment changed", err)
	}
}
