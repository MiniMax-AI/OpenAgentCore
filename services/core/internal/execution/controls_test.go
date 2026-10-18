package execution

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig"
	"github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig/harnesstest"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

func sessionConfiguration(agent string) json.RawMessage {
	return json.RawMessage(`{"agent":` + agent + `,"environment":{"type":"none"}}`)
}

// Admission asks the Harness declaration alone: Core holds no per-Harness rule.
func TestAdmissionValidatesOfficialOptionsAgainstTheDeclaration(t *testing.T) {
	declared := harnesstest.Declaration("declared", func(d *harnessconfig.Declaration) {
		d.Execution = harnessconfig.ExecutionOptions{
			WebSearchModes: []string{"live"}, WebSearchSettings: true, ReasoningEfforts: []string{"high"}, ReasoningSummaries: []string{"auto"},
			ServiceTiers: []string{"flex"}, ProgrammaticToolCalling: []bool{true}, TextVerbosities: []string{"medium", "high"},
		}
	})
	bare := harnesstest.Declaration("bare", nil)
	policy := Policy{Declarations: harnessconfig.NewRegistry(declared, bare)}
	for _, tc := range []struct {
		agent        string
		declared, ok bool
		field        string
	}{
		{`{"model":"m"}`, true, true, ""},
		{`{"model":"m"}`, false, true, ""},
		{`{"model":"m","tools":[{"type":"web_search","mode":"live","context_size":"high","allowed_domains":["a.example"],"location":{"city":"Paris"}}]}`, true, true, ""},
		{`{"model":"m","tools":[{"type":"web_search","mode":"live","context_size":"medium","allowed_domains":null,"location":null}]}`, false, false, "web_search.mode"},
		{`{"model":"m","tools":[{"type":"web_search","mode":"cached","context_size":"medium","allowed_domains":null,"location":null}]}`, true, false, "web_search.mode"},
		{`{"model":"m","tools":[{"type":"programmatic_tool_calling","enabled":true}]}`, true, true, ""},
		{`{"model":"m","tools":[{"type":"programmatic_tool_calling","enabled":false}]}`, true, false, "programmatic_tool_calling"},
		{`{"model":"m","tools":[{"type":"programmatic_tool_calling","enabled":true}]}`, false, false, "programmatic_tool_calling"},
		{`{"model":"m","reasoning":{"effort":"high","summary":"auto"},"service_tier":"flex","text":{"verbosity":"high"}}`, true, true, ""},
		{`{"model":"m","reasoning":{"effort":"max"}}`, true, false, "reasoning.effort"},
		{`{"model":"m","reasoning":{"summary":"concise"}}`, true, false, "reasoning.summary"},
		{`{"model":"m","service_tier":"priority"}`, true, false, "service_tier"},
		{`{"model":"m","service_tier":"auto"}`, false, true, ""},
		{`{"model":"m","service_tier":"flex"}`, false, false, "service_tier"},
		{`{"model":"m","text":{"verbosity":"low"}}`, true, false, "text.verbosity"},
	} {
		kind := "bare"
		if tc.declared {
			kind = "declared"
		}
		err := policy.ValidateSessionConfiguration(kind, sessionConfiguration(tc.agent))
		if (err == nil) != tc.ok || (err != nil && !strings.Contains(err.Error(), tc.field)) {
			t.Fatalf("%s %s: %v", kind, tc.agent, err)
		}
	}
}

func TestExecutionControlsCarryOnlyOfficialValues(t *testing.T) {
	enabled, disabled := true, false
	effort, summary := "max", "detailed"
	for _, tc := range []struct {
		name  string
		agent v1.Agent
		want  proto.ExecutionControls
	}{
		{"nothing set", v1.Agent{Model: "m", ServiceTier: "auto"}, proto.ExecutionControls{TextVerbosity: "medium"}},
		{"unset tier from an empty snapshot", v1.Agent{Model: "m"}, proto.ExecutionControls{TextVerbosity: "medium"}},
		{"set values", v1.Agent{Model: "m", ServiceTier: "flex", Reasoning: v1.Reasoning{Effort: &effort, Summary: &summary}, Text: v1.TextConfig{Verbosity: "low"}},
			proto.ExecutionControls{TextVerbosity: "low", ServiceTier: "flex", ReasoningEffort: "max", ReasoningSummary: "detailed"}},
		{"tools", v1.Agent{Model: "m", Tools: []json.RawMessage{
			json.RawMessage(`{"type":"web_search","mode":"cached","context_size":"high","allowed_domains":[],"location":{"city":"Paris","country":null,"region":null,"timezone":null}}`),
			json.RawMessage(`{"type":"programmatic_tool_calling","enabled":false}`)}},
			proto.ExecutionControls{TextVerbosity: "medium", ProgrammaticToolCalling: &disabled, WebSearch: &proto.WebSearchControl{Mode: "cached", ContextSize: "high", AllowedDomains: []string{},
				Location: &proto.SearchLocation{City: func() *string { s := "Paris"; return &s }()}}}},
		{"empty location is absent", v1.Agent{Model: "m", Tools: []json.RawMessage{
			json.RawMessage(`{"type":"web_search","mode":"live","context_size":"medium","allowed_domains":null,"location":{"city":null,"country":null,"region":null,"timezone":null}}`),
			json.RawMessage(`{"type":"programmatic_tool_calling","enabled":true}`)}},
			proto.ExecutionControls{TextVerbosity: "medium", ProgrammaticToolCalling: &enabled, WebSearch: &proto.WebSearchControl{Mode: "live", ContextSize: "medium"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snapshot := Snapshot{Agent: tc.agent}
			before, _ := json.Marshal(snapshot)
			for _, nativeID := range []string{"", "native-session"} {
				request, err := (&Dispatcher{}).executionRequest(t.Context(), sessions.Session{ID: "session"}, snapshot, runtimedevice.KindCapabilities{}, sessions.ExecutionBinding{NativeSessionID: nativeID})
				if err != nil || !reflect.DeepEqual(*request.ExecutionControls, tc.want) || request.AgentSessionID != nativeID {
					t.Fatalf("%+v %v", request.ExecutionControls, err)
				}
			}
			if after, _ := json.Marshal(snapshot); string(before) != string(after) {
				t.Fatal("snapshot mutated")
			}
		})
	}
}
