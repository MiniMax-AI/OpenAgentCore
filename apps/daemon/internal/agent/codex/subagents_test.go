package codex

import (
	"reflect"
	"testing"
)

func TestDisableSubagentsDisablesBothNativeFeatures(t *testing.T) {
	plan := SessionPlan{DisableFeatures: []string{"another", "multi_agent"}}
	disableSubagents(&plan)
	if !reflect.DeepEqual(plan.DisableFeatures, []string{"another", "multi_agent", "multi_agent_v2"}) {
		t.Fatal(plan.DisableFeatures)
	}
}
