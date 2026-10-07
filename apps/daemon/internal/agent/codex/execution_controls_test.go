package codex

import (
	"reflect"
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
	if want := [][2]string{{"tools.experimental_request_user_input.enabled", "false"}, {"web_search", `"disabled"`}, {"model_provider", `"` + oacProviderSlug + `"`}}; !reflect.DeepEqual(plan.ExtraConfig, want) {
		t.Fatal("native settings without ExecutionControls", plan.ExtraConfig)
	}
	for _, verbosity := range []string{"low", "medium", "high"} {
		plan, err := BuildSessionPlan(proto.PromptRequestPayload{Model: "fixture", ModelProvider: fixtureProvider(), AgentStateKey: "state", ExecutionControls: &proto.ExecutionControls{TextVerbosity: verbosity}})
		if err != nil {
			t.Fatal(err)
		}
		plan.Cleanup()
		want := [][2]string{{"tools.experimental_request_user_input.enabled", "false"}, {"web_search", `"disabled"`}, {"model_verbosity", `"` + verbosity + `"`}, {"model_provider", `"` + oacProviderSlug + `"`}}
		if !reflect.DeepEqual(plan.ExtraConfig, want) {
			t.Fatalf("config = %v, want %v", plan.ExtraConfig, want)
		}
	}
}

func TestExecutionControlsRejectIncompleteOrInvalidValues(t *testing.T) {
	t.Setenv("OAC_RUNTIME_HOME", t.TempDir())
	for _, controls := range []proto.ExecutionControls{{}, {TextVerbosity: "invalid"}} {
		if plan, err := BuildSessionPlan(proto.PromptRequestPayload{Model: "fixture", ModelProvider: fixtureProvider(), AgentStateKey: "state", ExecutionControls: &controls}); err == nil {
			plan.Cleanup()
			t.Fatal("invalid controls accepted", controls)
		}
	}
}
