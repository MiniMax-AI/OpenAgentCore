package codex

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

func TestSubagentProfileEnablesOnlyMultiAgent(t *testing.T) {
	limit := 6
	plan := SessionPlan{}
	if err := configureSubagentObservations(&plan, proto.PromptRequestPayload{ObserveSubagentIdentities: true, MaxConcurrentSubagents: &limit}); err != nil {
		t.Fatal(err)
	}
	want := [][2]string{{"features.multi_agent", "true"}, {"agents.max_threads", "6"}}
	if !slices.Equal(plan.EnableFeatures, []string{"multi_agent"}) || len(plan.DisableFeatures) != 0 || !slices.Equal(plan.ExtraConfig, want) {
		t.Fatal(plan)
	}
}

func TestSubagentProfileDoesNotDisableRequiredToolEnvironment(t *testing.T) {
	request := proto.PromptRequestPayload{ObserveSubagentIdentities: true, LocalEnvironment: &proto.LocalEnvironment{ToolEnvironment: true}}
	if err := configureSubagentObservations(&SessionPlan{}, request); err != nil {
		t.Fatal("ordinary environment must not require hooks", err)
	}
	request.DisableSubagents = true
	if err := configureSubagentObservations(&SessionPlan{}, request); err != nil {
		t.Fatal("single-agent tool environment changed", err)
	}
}

func TestSubagentProfileRejectsRequirementsThatForceUnobservableFeatures(t *testing.T) {
	for _, test := range []struct {
		name         string
		requirements any
		allowed      bool
	}{
		{"no requirements", nil, true},
		{"forced hooks", map[string]any{"featureRequirements": map[string]bool{"hooks": true}}, true},
		{"forced plugins", map[string]any{"featureRequirements": map[string]bool{"plugins": true}}, true},
		{"forced code mode", map[string]any{"featureRequirements": map[string]bool{"code_mode": true}}, false},
		{"forced multi_agent_v2", map[string]any{"featureRequirements": map[string]bool{"multi_agent_v2": true}}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			client, server, cleanup := NewTestClient()
			defer cleanup()
			done := make(chan error, 1)
			go func() { done <- verifySubagentObservationProfile(t.Context(), client.JSONRPCClient) }()
			decoder := json.NewDecoder(server.FromClient)
			encoder := json.NewEncoder(server.ToClient)
			for _, result := range []any{map[string]any{"requirements": test.requirements}} {
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

func TestSubagentItemSnapshotSkipsUnmappedItemTypes(t *testing.T) {
	for _, item := range []string{`{"type":"imageView","id":"i1","path":"/x.png"}`, `{"type":"contextCompaction","id":"c1"}`} {
		kind, _, _, err := subagentItemSnapshot(json.RawMessage(item), "completed", true)
		if err != nil || kind != "" {
			t.Fatal(item, kind, err)
		}
	}
}

func TestSubagentReasoningIsReportedOnlyWhenRequestedWithText(t *testing.T) {
	for _, test := range []struct {
		item      string
		requested bool
		want      string
	}{
		{`{"type":"reasoning","id":"r1","summary":["plan","next"]}`, true, `{"status":"completed","summary":[{"text":"plan","type":"summary_text"},{"text":"next","type":"summary_text"}]}`},
		{`{"type":"reasoning","id":"r1","summary":["plan"]}`, false, ""},
		{`{"type":"reasoning","id":"r1"}`, true, ""},
	} {
		kind, body, _, err := subagentItemSnapshot(json.RawMessage(test.item), "completed", test.requested)
		if err != nil || string(body) != test.want || (kind == "") != (test.want == "") {
			t.Fatal(test.item, test.requested, kind, string(body), err)
		}
	}
}
