package codex

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func TestSubagentProfileOverridesUnsafeNativeFeatures(t *testing.T) {
	limit := 6
	plan := SessionPlan{EnableFeatures: []string{"hooks", "code_mode", "plugins", "multi_agent_v2"}, DisableFeatures: []string{"multi_agent"}}
	if err := configureSubagentObservations(&plan, proto.PromptRequestPayload{ObserveSubagentIdentities: true, MaxConcurrentSubagents: &limit}); err != nil {
		t.Fatal(err)
	}
	for _, feature := range []string{"hooks", "plugins", "code_mode", "code_mode_only", "code_mode_prewarm", "multi_agent_v2"} {
		if slices.Contains(plan.EnableFeatures, feature) || !slices.Contains(plan.DisableFeatures, feature) {
			t.Fatal(feature, plan)
		}
	}
	if !slices.Contains(plan.ExtraConfig, [2]string{"agents.max_threads", "6"}) || !slices.Contains(plan.ExtraConfig, [2]string{"agents.max_depth", "64"}) {
		t.Fatal(plan.ExtraConfig)
	}
}

func TestSubagentProfileDoesNotDisableRequiredToolEnvironment(t *testing.T) {
	request := proto.PromptRequestPayload{ObserveSubagentIdentities: true, LocalEnvironment: &proto.LocalEnvironment{ToolEnvironment: true}}
	if err := configureSubagentObservations(&SessionPlan{}, request); err == nil {
		t.Fatal("incompatible hook profile accepted")
	}
	request.DisableSubagents = true
	if err := configureSubagentObservations(&SessionPlan{}, request); err != nil {
		t.Fatal("single-agent tool environment changed", err)
	}
}

func TestSubagentProfileRejectsRequirementsThatPermitResultRewrite(t *testing.T) {
	for _, test := range []struct {
		name         string
		requirements any
		allowed      bool
	}{
		{"ordinary disabled hooks", nil, true},
		{"managed-only hooks", map[string]any{"allowManagedHooksOnly": true, "featureRequirements": map[string]bool{"hooks": true}}, true},
		{"unrestricted forced hooks", map[string]any{"allowManagedHooksOnly": false, "featureRequirements": map[string]bool{"hooks": true}}, false},
		{"forced code mode", map[string]any{"featureRequirements": map[string]bool{"code_mode": true}}, false},
		{"forced plugins", map[string]any{"featureRequirements": map[string]bool{"plugins": true}}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			client, server, cleanup := NewTestClient()
			defer cleanup()
			done := make(chan error, 1)
			go func() { done <- verifySubagentObservationProfile(t.Context(), client.JSONRPCClient, "/workspace") }()
			decoder := json.NewDecoder(server.FromClient)
			encoder := json.NewEncoder(server.ToClient)
			for _, result := range []any{map[string]any{"data": []any{map[string]any{"cwd": "/workspace", "hooks": []any{}, "errors": []any{}}}}, map[string]any{"requirements": test.requirements}} {
				var request JsonRpcRequest
				if err := decoder.Decode(&request); err != nil {
					t.Fatal(err)
				}
				if err := encoder.Encode(map[string]any{"id": request.ID, "result": result}); err != nil {
					t.Fatal(err)
				}
			}
			if err := <-done; (err == nil) != test.allowed {
				t.Fatal("profile admission", err)
			}
		})
	}
}
