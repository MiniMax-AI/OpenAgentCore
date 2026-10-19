package codex

import (
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestPlanRejectsNonNativeFrozenProvider(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for _, protocol := range []string{"anthropic", "chat_completions"} {
		t.Run(protocol, func(t *testing.T) {
			provider := map[string]any{"protocol": protocol, "base_url": "https://model.invalid/v1", "api_key": "private-sentinel"}
			plan, err := BuildSessionPlan(testStateRoot(t), "frozen-state", map[string]any{"model": "frozen-model", "model_provider": provider}, nil)
			if plan.Cleanup != nil {
				plan.Cleanup()
			}
			if err == nil || !strings.Contains(err.Error(), "does not support") || strings.Contains(err.Error(), "private-sentinel") {
				t.Fatalf("non-native snapshot accepted: %v", err)
			}
			if !reflect.DeepEqual(provider, map[string]any{"protocol": protocol, "base_url": "https://model.invalid/v1", "api_key": "private-sentinel"}) {
				t.Fatal("frozen provider was rewritten")
			}
		})
	}
}

func TestPlanRejectsIncompleteExplicitProvider(t *testing.T) {
	for _, options := range []map[string]any{
		{"model": "chosen", "model_provider": nil},
		{"model_provider": map[string]any{"protocol": "responses", "base_url": "https://model.example/v1", "api_key": "fixture"}},
	} {
		if _, err := BuildSessionPlan(testStateRoot(t), "state", options, nil); err == nil {
			t.Fatal("explicit provider fell back to native defaults")
		}
	}
}

func TestPlanMapsTheContextWindowNatively(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	provider := map[string]any{"protocol": "responses", "base_url": "https://model.example/v1", "api_key": "fixture", "context_window": 64000}
	plan, err := BuildSessionPlan(testStateRoot(t), "limits", map[string]any{"model": "m", "model_provider": provider}, nil)
	if err != nil || !slices.Contains(plan.ExtraConfig, [2]string{"model_context_window", "64000"}) {
		t.Fatalf("context window not native: %v %v", plan.ExtraConfig, err)
	}
	plan.Cleanup()
	provider["max_output_tokens"] = 4096
	if _, err := BuildSessionPlan(testStateRoot(t), "limits", map[string]any{"model": "m", "model_provider": provider}, nil); err == nil {
		t.Fatal("max_output_tokens without a native home accepted")
	}
}
