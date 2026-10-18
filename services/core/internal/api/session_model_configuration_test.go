package api

import (
	"context"
	"encoding/json"
	"testing"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/modelconfiguration"
)

func TestSessionNativeConfigurationSources(t *testing.T) {
	provider := fixtureModelProvider("claude_sdk")
	saved := &v1.SavedAgent{SavedAgentConfiguration: v1.SavedAgentConfiguration{Model: "saved-model", XAgentsCore: &v1.SavedAgentCore{Harness: "claude_sdk", HarnessConfig: json.RawMessage(`{"thinking":{"type":"enabled","budgetTokens":1000}}`)}}}
	for _, tc := range []struct {
		name, body            string
		saved                 *v1.SavedAgent
		model, native, source string
		wantError             bool
	}{
		{"deployment", `{"agent":{},"environment":{"type":"openai_hosted"}}`, nil, "deployment-model", `{"thinking":{"type":"enabled","budgetTokens":3000}}`, "deployment", false},
		{"none deployment", `{"agent":{},"environment":{"type":"none"}}`, nil, "deployment-model", `{"thinking":{"type":"enabled","budgetTokens":3000}}`, "deployment", false},
		{"saved", `{"agent_id":"a","environment":{"type":"openai_hosted"}}`, saved, "saved-model", `{"thinking":{"type":"enabled","budgetTokens":1000}}`, "agent", false},
		{"model override discards", `{"agent_id":"a","agent":{"model":"other"},"environment":{"type":"openai_hosted"}}`, saved, "other", `{}`, "session", false},
		{"explicit model discards deployment", `{"agent":{"model":"other"},"environment":{"type":"openai_hosted"}}`, nil, "other", `{}`, "session", false},
		{"explicit clear", `{"agent_id":"a","environment":{"type":"openai_hosted"},"x_agents_core":{"harness_config":{}}}`, saved, "saved-model", `{}`, "session", false},
		{"inline native", `{"agent":{"model":"other","x_agents_core":{"harness_config":{"thinking":{"type":"enabled","budgetTokens":2000}}}},"environment":{"type":"openai_hosted"}}`, nil, "other", `{"thinking":{"type":"enabled","budgetTokens":2000}}`, "session", false},
		{"session beats inline", `{"agent":{"model":"other","x_agents_core":{"harness_config":{"thinking":{"type":"enabled","budgetTokens":2000}}}},"environment":{"type":"openai_hosted"},"x_agents_core":{"harness_config":{}}}`, nil, "other", `{}`, "session", false},
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
			deps.Engine = "claude_sdk"
			fakes.modelProviders.resolve = func(context.Context, string) (*modelconfiguration.Snapshot, error) {
				calls++
				return &modelconfiguration.Snapshot{Provider: provider, Model: "deployment-model", HarnessConfig: json.RawMessage(`{"thinking":{"type":"enabled","budgetTokens":3000}}`)}, nil
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
			if value != tc.model || string(input.resolvedHarnessConfig) != tc.native || string(input.harnessConfigSource) != tc.source {
				t.Fatalf("model=%s native=%s source=%s", value, input.resolvedHarnessConfig, input.harnessConfigSource)
			}
		})
	}
}

func TestProviderReplacementDiscardsNativeConfiguration(t *testing.T) {
	provider := fixtureModelProvider("claude_sdk")
	model := "saved-model"
	input := sessionRequest{CreateSessionRequest: v1.CreateSessionRequest{Agent: &v1.InlineAgent{Model: nil}, Environment: &v1.Environment{Type: "self_hosted"}, XAgentsCore: &v1.SessionExecutionInput{ModelProvider: provider}}}
	saved := &v1.SavedAgent{SavedAgentConfiguration: v1.SavedAgentConfiguration{Model: model, XAgentsCore: &v1.SavedAgentCore{Harness: "claude_sdk", HarnessConfig: json.RawMessage(`{"thinking":{"type":"enabled","budgetTokens":3000}}`)}}}
	deps, _ := testDependencies(t)
	deps.Engine = "claude_sdk"
	h := &Handler{Dependencies: deps}
	if err := h.prepareSessionModelConfiguration(t.Context(), &input, saved, provider); err != nil {
		t.Fatal(err)
	}
	if string(input.resolvedHarnessConfig) != "{}" {
		t.Fatal("new provider inherited old native parameters")
	}
}
