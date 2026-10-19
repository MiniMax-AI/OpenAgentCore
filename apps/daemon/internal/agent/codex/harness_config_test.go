package codex

import (
	"context"
	"encoding/json"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig"
	"slices"
	"strings"
	"testing"
)

func TestReasoningEffortAppliedWithoutChangingProvider(t *testing.T) {
	t.Setenv("OAC_RUNTIME_HOME", t.TempDir())
	plan, err := BuildSessionPlan(testStateRoot(t), "native-config", map[string]any{
		"model":          "fixture",
		"model_provider": map[string]any{"base_url": "https://provider.invalid/v1", "protocol": "responses", "api_key": "test-key"},
	}, &proto.ExecutionControls{ReasoningEffort: "max"})
	if err != nil {
		t.Fatal(err)
	}
	defer plan.Cleanup()
	if plan.Model != "fixture" || plan.ModelProvider != oacProviderSlug || !slices.Contains(plan.ExtraConfig, [2]string{"model_reasoning_effort", `"max"`}) {
		t.Fatalf("native configuration not applied: %+v", plan.ExtraConfig)
	}
}

func TestReasoningEffortHasNoHarnessConfigHome(t *testing.T) {
	_, err := BuildSessionPlan(testStateRoot(t), "", map[string]any{"harness_config": map[string]any{"model_reasoning_effort": "high"}}, nil)
	if err != harnessconfig.ErrHarnessConfig {
		t.Fatalf("duplicate effort home accepted: %v", err)
	}
}

func TestHarnessConfigConflictFailsBeforePreparation(t *testing.T) {
	_, err := BuildSessionPlan(testStateRoot(t), "", map[string]any{"harness_config": map[string]any{"model_provider": "bypass"}}, nil)
	if err != harnessconfig.ErrHarnessConfig {
		t.Fatalf("configuration must fail before filesystem preparation: %v", err)
	}
}

func TestNativeTurnsCarryNoCollaborationMode(t *testing.T) {
	for _, resumeID := range []string{"", "fixture-native-thread"} {
		t.Run("resume="+resumeID, func(t *testing.T) {
			req, cfg, root := preparationFixture(t)
			t.Setenv("OAC_TEST_EXECUTOR_MODE", "complete")
			req.AgentSessionID = resumeID
			controls := &proto.ExecutionControls{TextVerbosity: "medium", ReasoningEffort: "high"}
			req.ExecutionControls = controls
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
			controls.ReasoningEffort = "low"
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
				if strings.Contains(string(frame.Params), "ollaborationMode") {
					t.Fatal("turn/start must not carry a collaboration mode", string(frame.Params))
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

func TestOutputSchemaReachesEveryNativeTurn(t *testing.T) {
	for _, resumeID := range []string{"", "fixture-native-thread"} {
		t.Run("resume="+resumeID, func(t *testing.T) {
			req, cfg, root := preparationFixture(t)
			t.Setenv("OAC_TEST_EXECUTOR_MODE", "complete")
			req.AgentSessionID = resumeID
			schema := json.RawMessage(`{"type":"object","properties":{"answer":{"type":"string"}},"required":["answer"],"additionalProperties":false}`)
			req.ExecutionControls = &proto.ExecutionControls{OutputFormat: &proto.OutputFormat{Type: "json_schema", Schema: schema}}
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
			for _, id := range []string{"one", "two"} {
				out := make(chan proto.Envelope, 20)
				turn, err := e.StartTurn(t.Context(), id, proto.TextInput("answer"), out)
				if err != nil {
					t.Fatal(err)
				}
				awaitExecutorTurn(t, turn, out)
			}
			turns := 0
			for _, frame := range preparationFrames(t, root) {
				if frame.Method != "turn/start" {
					continue
				}
				turns++
				var wire struct {
					OutputSchema json.RawMessage `json:"outputSchema"`
				}
				if json.Unmarshal(frame.Params, &wire) != nil || string(wire.OutputSchema) != string(schema) {
					t.Fatal("Turn lost the frozen output schema", string(frame.Params))
				}
			}
			if turns != 2 {
				t.Fatal("expected two Turns", turns)
			}
		})
	}
}
