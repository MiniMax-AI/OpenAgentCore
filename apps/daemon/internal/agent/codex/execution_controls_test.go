package codex

import (
	"reflect"
	"slices"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

func ptr[T any](v T) *T { return &v }

func TestExecutionControlsMapToNativeSettings(t *testing.T) {
	t.Setenv("OAC_RUNTIME_HOME", t.TempDir())
	base := [][2]string{{"tools.experimental_request_user_input.enabled", "false"}}
	for _, tc := range []struct {
		name     string
		controls *proto.ExecutionControls
		want     [][2]string
	}{
		{"absent controls turn summaries off", nil, nil},
		{"empty controls turn summaries off", &proto.ExecutionControls{}, nil},
		{"verbosity", &proto.ExecutionControls{TextVerbosity: "high"}, [][2]string{{"model_verbosity", `"high"`}}},
		{"search without settings", &proto.ExecutionControls{WebSearch: &proto.WebSearchControl{Mode: "live", ContextSize: "medium"}},
			[][2]string{{"web_search", `"live"`}, {"tools.web_search.context_size", `"medium"`}}},
		{"search settings", &proto.ExecutionControls{WebSearch: &proto.WebSearchControl{Mode: "cached", ContextSize: "high", AllowedDomains: []string{"a.example", "b.example"},
			Location: &proto.SearchLocation{City: ptr("Paris"), Timezone: ptr("Europe/Paris")}}},
			[][2]string{{"web_search", `"cached"`}, {"tools.web_search.context_size", `"high"`}, {"tools.web_search.allowed_domains", `["a.example","b.example"]`},
				{"tools.web_search.location.city", `"Paris"`}, {"tools.web_search.location.timezone", `"Europe/Paris"`}}},
		{"empty domain list stays distinct from null", &proto.ExecutionControls{WebSearch: &proto.WebSearchControl{Mode: "disabled", ContextSize: "low", AllowedDomains: []string{}}},
			[][2]string{{"web_search", `"disabled"`}, {"tools.web_search.context_size", `"low"`}, {"tools.web_search.allowed_domains", `[]`}}},
		{"reasoning summary", &proto.ExecutionControls{ReasoningSummary: "concise"}, [][2]string{{"model_reasoning_summary", `"concise"`}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan, err := BuildSessionPlan(testStateRoot(t), "state", map[string]any{}, proto.TurnSettings{}, tc.controls)
			if err != nil {
				t.Fatal(err)
			}
			plan.Cleanup()
			want := append([][2]string{}, base...)
			if tc.controls == nil || tc.controls.ReasoningSummary == "" {
				want = append(want, [2]string{"model_reasoning_summary", `"none"`})
			}
			if want = append(want, tc.want...); !reflect.DeepEqual(plan.ExtraConfig, want) {
				t.Fatalf("config = %v, want %v", plan.ExtraConfig, want)
			}
			if plan.ReasoningSummary != (tc.controls != nil && tc.controls.ReasoningSummary != "") {
				t.Fatalf("reasoning summary gate = %v", plan.ReasoningSummary)
			}
		})
	}
}

func TestExecutionControlsRejectUndeclaredValues(t *testing.T) {
	t.Setenv("OAC_RUNTIME_HOME", t.TempDir())
	for _, controls := range []proto.ExecutionControls{
		{WebSearch: &proto.WebSearchControl{Mode: "indexed", ContextSize: "medium"}},
		{TextVerbosity: "invalid"}, {ReasoningSummary: "none"},
	} {
		if plan, err := BuildSessionPlan(testStateRoot(t), "state", nil, proto.TurnSettings{}, &controls); err == nil {
			plan.Cleanup()
			t.Fatal("undeclared controls accepted", controls)
		}
	}
}

func TestTurnSettingsReachNativeConfigAndRejectUndeclaredValues(t *testing.T) {
	t.Setenv("OAC_RUNTIME_HOME", t.TempDir())
	plan, err := BuildSessionPlan(testStateRoot(t), "state", nil, proto.TurnSettings{Model: "m", ReasoningEffort: "max", ServiceTier: "priority"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	plan.Cleanup()
	if plan.Model != "m" || !slices.Contains(plan.ExtraConfig, [2]string{"model_reasoning_effort", `"max"`}) || !slices.Contains(plan.ExtraConfig, [2]string{"service_tier", `"priority"`}) {
		t.Fatalf("turn settings not applied: %v", plan.ExtraConfig)
	}
	for _, turn := range []proto.TurnSettings{{ReasoningEffort: "ultra"}, {ServiceTier: "scale"}} {
		if plan, err := BuildSessionPlan(testStateRoot(t), "state", nil, turn, nil); err == nil {
			plan.Cleanup()
			t.Fatal("undeclared turn setting accepted", turn)
		}
	}
}
