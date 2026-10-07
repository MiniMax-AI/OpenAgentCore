package codex

import (
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

func TestExecutionControlsSelectNativeSettings(t *testing.T) {
	t.Setenv("OAC_RUNTIME_HOME", t.TempDir())
	plan, err := BuildSessionPlan(proto.PromptRequestPayload{Model: "fixture", ModelProvider: fixtureProvider(), AgentStateKey: "state"})
	if err != nil {
		t.Fatal(err)
	}
	plan.Cleanup()
	if len(plan.ExtraConfig) != 1 || plan.ExtraConfig[0][0] != "model_provider" {
		t.Fatal("native settings without ExecutionControls", plan.ExtraConfig)
	}
	for _, search := range []string{"disabled", "cached", "live"} {
		for _, verbosity := range []string{"low", "medium", "high"} {
			plan, err := BuildSessionPlan(proto.PromptRequestPayload{Model: "fixture", ModelProvider: fixtureProvider(), AgentStateKey: "state", ExecutionControls: &proto.ExecutionControls{WebSearch: search, TextVerbosity: verbosity}})
			if err != nil {
				t.Fatal(err)
			}
			want := map[string]string{"web_search": `"` + search + `"`, "model_verbosity": `"` + verbosity + `"`}
			for _, kv := range plan.ExtraConfig {
				if v, ok := want[kv[0]]; ok {
					if kv[1] != v {
						t.Fatal("native control differs", kv)
					}
					delete(want, kv[0])
				}
			}
			plan.Cleanup()
			if len(want) != 0 {
				t.Fatal("native settings omitted", want)
			}
		}
	}
}

func TestExecutionControlsRejectIncompleteOrInvalidValues(t *testing.T) {
	t.Setenv("OAC_RUNTIME_HOME", t.TempDir())
	for _, controls := range []proto.ExecutionControls{
		{}, {WebSearch: "disabled"}, {TextVerbosity: "medium"},
		{WebSearch: "invalid", TextVerbosity: "medium"}, {WebSearch: "disabled", TextVerbosity: "invalid"},
	} {
		if plan, err := BuildSessionPlan(proto.PromptRequestPayload{Model: "fixture", ModelProvider: fixtureProvider(), AgentStateKey: "state", ExecutionControls: &controls}); err == nil {
			plan.Cleanup()
			t.Fatal("invalid controls accepted", controls)
		}
	}
}
