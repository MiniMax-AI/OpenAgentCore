package codex

import (
	"reflect"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

func TestExecutionControlsSelectNativeSettings(t *testing.T) {
	t.Setenv("OAC_RUNTIME_HOME", t.TempDir())
	for _, search := range []string{"disabled", "cached", "live"} {
		for _, verbosity := range []string{"low", "medium", "high"} {
			plan, err := BuildSessionPlan(proto.PromptRequestPayload{AgentStateKey: "state", ExecutionControls: &proto.ExecutionControls{WebSearch: search, TextVerbosity: verbosity}})
			if err != nil {
				t.Fatal(err)
			}
			plan.Cleanup()
			want := [][2]string{{"tools.experimental_request_user_input.enabled", "false"}, {"web_search", `"` + search + `"`}, {"model_verbosity", `"` + verbosity + `"`}}
			if !reflect.DeepEqual(plan.ExtraConfig, want) {
				t.Fatalf("config = %v, want %v", plan.ExtraConfig, want)
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
		if plan, err := BuildSessionPlan(proto.PromptRequestPayload{AgentStateKey: "state", ExecutionControls: &controls}); err == nil {
			plan.Cleanup()
			t.Fatal("invalid controls accepted", controls)
		}
	}
}
