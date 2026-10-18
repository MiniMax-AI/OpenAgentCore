package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

func TestProgrammaticToolsFollowTheOfficialValue(t *testing.T) {
	enabled, disabled := true, false
	plan := SessionPlan{ExtraConfig: [][2]string{{"features.code_mode", "true"}}}
	applyProgrammaticTools(&plan, nil)
	applyProgrammaticTools(&plan, &proto.ExecutionControls{})
	if len(plan.DisableFeatures) != 0 || len(plan.EnableFeatures) != 0 || len(plan.ExtraConfig) != 1 {
		t.Fatal("omission changed native configuration")
	}
	applyProgrammaticTools(&plan, &proto.ExecutionControls{ProgrammaticToolCalling: &enabled})
	if !slices.Equal(plan.EnableFeatures, []string{"code_mode"}) || len(plan.DisableFeatures) != 0 || plan.ExtraConfig[len(plan.ExtraConfig)-1] != [2]string{"features.code_mode", "true"} {
		t.Fatal("enabling did not select code mode", plan)
	}
	applyProgrammaticTools(&plan, &proto.ExecutionControls{ProgrammaticToolCalling: &disabled})
	for _, feature := range programmaticFeatures {
		if !slices.Contains(plan.DisableFeatures, feature) {
			t.Fatal("missing disable", feature)
		}
		value := ""
		for _, config := range plan.ExtraConfig {
			if config[0] == "features."+feature {
				value = config[1]
			}
		}
		if value != "false" {
			t.Fatal("final override missing", feature)
		}
	}
}

func TestProgrammaticToolsRejectManagedOverridesBeforeExecution(t *testing.T) {
	for _, tc := range []struct {
		response string
		enabled  bool
		accepted bool
	}{
		{`{"requirements":null}`, false, true},
		{`{"requirements":{"featureRequirements":{"code_mode":false,"unrelated":true}}}`, false, true},
		{`{"requirements":{"featureRequirements":{"code_mode":true}}}`, false, false},
		{`{"requirements":{"featureRequirements":{"code_mode_only":true}}}`, false, false},
		{`{"requirements":{"featureRequirements":{"code_mode_prewarm":true}}}`, false, false},
		{`{}`, false, false}, {`null`, false, false}, {`{"requirements":[]}`, false, false},
		{`{"requirements":null}`, true, true},
		{`{"requirements":{"featureRequirements":{"code_mode":true}}}`, true, true},
		{`{"requirements":{"featureRequirements":{"code_mode":false}}}`, true, false},
	} {
		t.Run(fmt.Sprint(tc.enabled, tc.response), func(t *testing.T) {
			client, server, cleanup := NewTestClient()
			defer cleanup()
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- verifyProgrammaticTools(ctx, client.JSONRPCClient, tc.enabled) }()
			var request struct{ ID, Method string }
			if err := json.NewDecoder(server.FromClient).Decode(&request); err != nil {
				t.Fatal(err)
			}
			if request.Method != "configRequirements/read" {
				t.Fatal(request.Method)
			}
			if err := json.NewEncoder(server.ToClient).Encode(map[string]any{"id": request.ID, "result": json.RawMessage(tc.response)}); err != nil {
				t.Fatal(err)
			}
			if err := <-done; (err == nil) != tc.accepted {
				t.Fatal(err)
			}
		})
	}
}
