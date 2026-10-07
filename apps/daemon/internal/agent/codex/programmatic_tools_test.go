package codex

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

func TestProgrammaticToolsExplicitDisableOverridesNativeOptions(t *testing.T) {
	plan := SessionPlan{ExtraConfig: [][2]string{{"features.code_mode", "true"}}}
	disableProgrammaticTools(&plan, nil)
	if len(plan.DisableFeatures) != 0 || len(plan.ExtraConfig) != 1 {
		t.Fatal("omission changed native configuration")
	}
	disableProgrammaticTools(&plan, &proto.ExecutionControls{DisableProgrammaticToolCalling: true})
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
		accepted bool
	}{
		{`{"requirements":null}`, true},
		{`{"requirements":{"featureRequirements":{"code_mode":false,"unrelated":true}}}`, true},
		{`{"requirements":{"featureRequirements":{"code_mode":true}}}`, false},
		{`{"requirements":{"featureRequirements":{"code_mode_only":true}}}`, false},
		{`{"requirements":{"featureRequirements":{"code_mode_prewarm":true}}}`, false},
		{`{}`, false}, {`null`, false}, {`{"requirements":[]}`, false},
	} {
		t.Run(tc.response, func(t *testing.T) {
			client, server, cleanup := NewTestClient()
			defer cleanup()
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- verifyProgrammaticToolsDisabled(ctx, client.JSONRPCClient) }()
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
