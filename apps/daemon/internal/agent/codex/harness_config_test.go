package codex

import (
	"context"
	"encoding/json"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig"
	"slices"
	"testing"
)

func TestHarnessConfigAppliedWithoutChangingProvider(t *testing.T) {
	t.Setenv("OAC_RUNTIME_HOME", t.TempDir())
	plan, err := BuildSessionPlan("run", "native-config", "", map[string]any{
		"model": "fixture", "harness_config": map[string]any{"model_reasoning_effort": "high"},
		"model_provider": map[string]any{"base_url": "https://provider.invalid/v1", "protocol": "responses", "api_key": "test-key"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer plan.Cleanup()
	if plan.Model != "fixture" || plan.ModelProvider != oacProviderSlug || !slices.Contains(plan.ExtraConfig, [2]string{"model_reasoning_effort", `"high"`}) {
		t.Fatalf("native configuration not applied: %+v", plan.ExtraConfig)
	}
}

func TestHarnessConfigConflictFailsBeforePreparation(t *testing.T) {
	_, err := BuildSessionPlan("run", "", "", map[string]any{"harness_config": map[string]any{"model_provider": "bypass"}})
	if err != harnessconfig.ErrHarnessConfig {
		t.Fatalf("configuration must fail before filesystem preparation: %v", err)
	}
}

func TestHarnessConfigReachesEveryNativeTurn(t *testing.T) {
	for _, resumeID := range []string{"", "fixture-native-thread"} {
		t.Run("resume="+resumeID, func(t *testing.T) {
			req, cfg, root := preparationFixture(t)
			t.Setenv("OAC_TEST_EXECUTOR_MODE", "complete")
			req.AgentSessionID = resumeID
			native := map[string]any{"model_reasoning_effort": "high"}
			req.AgentOptions["harness_config"] = native
			ownerCtx, cancelOwner := context.WithCancel(context.Background())
			t.Cleanup(cancelOwner)
			e, err := newExecutor(ownerCtx, req, cfg)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if err := e.Close(ctx); err != nil {
					t.Error(err)
				}
			})
			native["model_reasoning_effort"] = "low"
			for _, id := range []string{"one", "two"} {
				out := make(chan proto.Envelope, 20)
				turn, err := e.StartTurn(t.Context(), id, proto.TextInput("answer"), out)
				if err != nil {
					t.Fatal(err)
				}
				if !awaitExecutorTurn(t, turn, out).Reusable {
					t.Fatal("healthy Turn was not reusable")
				}
			}
			counts := map[string]int{}
			for _, frame := range preparationFrames(t, root) {
				counts[frame.Method]++
				if frame.Method != "turn/start" {
					continue
				}
				var wire struct {
					CollaborationMode struct {
						Settings map[string]any `json:"settings"`
					} `json:"collaborationMode"`
				}
				if json.Unmarshal(frame.Params, &wire) != nil || wire.CollaborationMode.Settings["reasoning_effort"] != "high" {
					t.Fatal("Turn lost the frozen native reasoning effort", string(frame.Params))
				}
			}
			method := "thread/start"
			if resumeID != "" {
				method = "thread/resume"
			}
			if counts[method] != 1 || counts["turn/start"] != 2 {
				t.Fatal("expected one native thread and two Turns", counts)
			}
		})
	}
}
