package api

import (
	"context"
	"encoding/json"
	"testing"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
)

func TestSessionNativeConfigurationSources(t *testing.T) {
	provider := fixtureModelProvider("codex")
	saved := &v1.SavedAgent{SavedAgentConfiguration: v1.SavedAgentConfiguration{Model: "saved-model", XAgentsCore: &v1.SavedAgentCore{Harness: "codex", HarnessConfig: json.RawMessage(`{"model_reasoning_effort":"low"}`)}}}
	for _, tc := range []struct {
		name, body            string
		saved                 *v1.SavedAgent
		model, native, source string
		wantError             bool
	}{
		{"deployment", `{"agent":{},"environment":{"type":"openai_hosted"}}`, nil, "deployment-model", `{"model_reasoning_effort":"high"}`, "deployment", false},
		{"none deployment", `{"agent":{},"environment":{"type":"none"}}`, nil, "deployment-model", `{"model_reasoning_effort":"high"}`, "deployment", false},
		{"saved", `{"agent_id":"a","environment":{"type":"openai_hosted"}}`, saved, "saved-model", `{"model_reasoning_effort":"low"}`, "agent", false},
		{"model override discards", `{"agent_id":"a","agent":{"model":"other"},"environment":{"type":"openai_hosted"}}`, saved, "other", `{}`, "session", false},
		{"explicit model discards deployment", `{"agent":{"model":"other"},"environment":{"type":"openai_hosted"}}`, nil, "other", `{}`, "session", false},
		{"explicit clear", `{"agent_id":"a","environment":{"type":"openai_hosted"},"x_agents_core":{"harness_config":{}}}`, saved, "saved-model", `{}`, "session", false},
		{"inline native", `{"agent":{"model":"other","x_agents_core":{"harness_config":{"model_reasoning_effort":"medium"}}},"environment":{"type":"openai_hosted"}}`, nil, "other", `{"model_reasoning_effort":"medium"}`, "session", false},
		{"session beats inline", `{"agent":{"model":"other","x_agents_core":{"harness_config":{"model_reasoning_effort":"medium"}}},"environment":{"type":"openai_hosted"},"x_agents_core":{"harness_config":{}}}`, nil, "other", `{}`, "session", false},
		{"null rejects", `{"agent":{"model":"other"},"environment":{"type":"openai_hosted"},"x_agents_core":{"harness_config":null}}`, nil, "", "", "", true},
		{"reserved rejects", `{"agent":{"model":"other"},"environment":{"type":"openai_hosted"},"x_agents_core":{"harness_config":{"api_key":"secret"}}}`, nil, "", "", "", true},
		{"self hosted never defaults", `{"agent":{},"environment":{"type":"self_hosted","workspace_directory":"/tmp/work"}}`, nil, "", "", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var decoded decodedSessionRequest
			if err := json.Unmarshal([]byte(tc.body), &decoded); err != nil {
				t.Fatal(err)
			}
			input, err := decoded.validated()
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			deps, fakes := testDependencies(t)
			fakes.modelProviders.deploymentModelProvider = func(context.Context, string) (*store.DeploymentModelProviderSnapshot, error) {
				calls++
				return &store.DeploymentModelProviderSnapshot{Provider: provider, Model: "deployment-model", HarnessConfig: json.RawMessage(`{"model_reasoning_effort":"high"}`)}, nil
			}
			h := &Handler{Dependencies: deps}
			err = h.prepareSessionModelConfiguration(t.Context(), &input, tc.saved, nil)
			if (err != nil) != tc.wantError {
				t.Fatalf("error=%v", err)
			}
			if input.Environment.Type == "self_hosted" && calls != 0 {
				t.Fatal("self-hosted accessed deployment credentials")
			}
			if tc.wantError {
				return
			}
			model := tc.saved
			value := ""
			if model != nil {
				value = model.Model
			}
			if input.Agent != nil && input.Agent.Model != nil {
				value = *input.Agent.Model
			}
			if value != tc.model || string(input.resolvedHarnessConfig) != tc.native || input.harnessConfigSource != tc.source {
				t.Fatalf("model=%s native=%s source=%s", value, input.resolvedHarnessConfig, input.harnessConfigSource)
			}
		})
	}
}

func TestProviderReplacementDiscardsNativeConfiguration(t *testing.T) {
	provider := fixtureModelProvider("codex")
	model := "saved-model"
	input := sessionRequest{CreateSessionRequest: v1.CreateSessionRequest{Agent: &v1.InlineAgent{Model: nil}, Environment: &v1.Environment{Type: "self_hosted"}, XAgentsCore: &v1.SessionExecutionInput{ModelProvider: provider}}}
	saved := &v1.SavedAgent{SavedAgentConfiguration: v1.SavedAgentConfiguration{Model: model, XAgentsCore: &v1.SavedAgentCore{Harness: "codex", HarnessConfig: json.RawMessage(`{"model_reasoning_effort":"high"}`)}}}
	deps, _ := testDependencies(t)
	h := &Handler{Dependencies: deps}
	if err := h.prepareSessionModelConfiguration(t.Context(), &input, saved, provider); err != nil {
		t.Fatal(err)
	}
	if string(input.resolvedHarnessConfig) != "{}" {
		t.Fatal("new provider inherited old native parameters")
	}
}
