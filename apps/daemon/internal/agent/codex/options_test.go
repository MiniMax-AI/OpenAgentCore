package codex

import (
	"slices"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

func TestBuildSessionPlan_DefaultsToBypass(t *testing.T) {
	plan, err := BuildSessionPlan(prepared(t, "conv-1/agent-1/codex", proto.PromptRequestPayload{Model: "fixture", ModelProvider: fixtureProvider()}))
	if err != nil {
		t.Fatalf("BuildSessionPlan: %v", err)
	}
	if !slices.Contains(plan.ExtraConfig, [2]string{"tools.experimental_request_user_input.enabled", "false"}) {
		t.Fatalf("default plan must disable the native ask-the-user tool, got %+v", plan.ExtraConfig)
	}
	if plan.Cleanup == nil {
		t.Fatal("Cleanup must be non-nil")
	}
	plan.Cleanup()
}

func TestBuildSessionPlan_AllocsCodexHomeAndEnv(t *testing.T) {
	plan, err := BuildSessionPlan(prepared(t, "conv-1/agent-1/codex", proto.PromptRequestPayload{Model: "fixture", ModelProvider: fixtureProvider()}))
	if err != nil {
		t.Fatalf("BuildSessionPlan: %v", err)
	}
	defer plan.Cleanup()
	hasCodexHome := false
	hasTelemetry := false
	for _, kv := range plan.Env {
		switch {
		case strings.HasPrefix(kv, "CODEX_HOME="):
			hasCodexHome = true
		case kv == "DISABLE_TELEMETRY=1":
			hasTelemetry = true
		}
	}
	if !hasCodexHome {
		t.Fatalf("env missing CODEX_HOME: %+v", plan.Env)
	}
	if !hasTelemetry {
		t.Fatalf("env missing DISABLE_TELEMETRY: %+v", plan.Env)
	}
}

func TestBuildSessionPlan_StableCodexHomeByStateKey(t *testing.T) {
	stateKey := "conv-stable/agent-stable/codex"
	planA, err := BuildSessionPlan(prepared(t, stateKey, proto.PromptRequestPayload{Model: "fixture", ModelProvider: fixtureProvider()}))
	if err != nil {
		t.Fatalf("BuildSessionPlan A: %v", err)
	}
	planB, err := BuildSessionPlan(prepared(t, stateKey, proto.PromptRequestPayload{Model: "fixture", ModelProvider: fixtureProvider()}))
	if err != nil {
		t.Fatalf("BuildSessionPlan B: %v", err)
	}
	if codexHomeFromEnv(planA.Env) == "" || codexHomeFromEnv(planA.Env) != codexHomeFromEnv(planB.Env) {
		t.Fatalf("CODEX_HOME must be stable by state key: A=%q B=%q", codexHomeFromEnv(planA.Env), codexHomeFromEnv(planB.Env))
	}
}

func codexHomeFromEnv(env []string) string {
	for _, kv := range env {
		if strings.HasPrefix(kv, "CODEX_HOME=") {
			return strings.TrimPrefix(kv, "CODEX_HOME=")
		}
	}
	return ""
}

func TestBuildSessionPlan_CarriesModelAndSystemPrompt(t *testing.T) {
	t.Setenv("OAC_RUNTIME_HOME", t.TempDir())
	plan, err := BuildSessionPlan(prepared(t, "conv/agent/codex", proto.PromptRequestPayload{ModelProvider: fixtureProvider(), Model: "MiniMax-M3", SystemPrompt: "current reference"}))
	if err != nil {
		t.Fatal(err)
	}
	defer plan.Cleanup()
	if plan.SystemPrompt != "current reference" || plan.Model != "MiniMax-M3" {
		t.Fatalf("plan did not carry the default turn instructions: %+v", plan)
	}
}

func TestNativeInputPreservesText(t *testing.T) {
	inputs, err := nativeInput(proto.TextInput("  hello world  "))
	if err != nil {
		t.Fatal(err)
	}
	if len(inputs) != 1 {
		t.Fatalf("len = %d", len(inputs))
	}
	if inputs[0].Type != UserInputText || inputs[0].Text != "  hello world  " {
		t.Fatalf("input = %+v", inputs[0])
	}
}

func TestFirstUserInput_EmptyReturnsNil(t *testing.T) {
	if got, err := nativeInput(proto.TextInput("")); err == nil {
		t.Fatalf("empty prompt must return nil, got %+v", got)
	}
}

// The shared validator admits whitespace-only text; Codex receives it unchanged.
func TestFirstUserInput_WhitespaceIsUnchanged(t *testing.T) {
	inputs, err := nativeInput(append(proto.TextInput("   "), proto.TextInput("\n\t")...))
	if err != nil || len(inputs) != 3 || inputs[0].Text != "   " || inputs[1].Text != "\n\n" || inputs[2].Text != "\n\t" {
		t.Fatalf("input = %+v, %v", inputs, err)
	}
}
