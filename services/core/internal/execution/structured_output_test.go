package execution

import (
	"context"
	"encoding/json"
	"testing"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/device"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/engine"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
)

func TestStructuredOutputNeedsOperationQualification(t *testing.T) {
	raw := json.RawMessage(`{"agent":{"model":"model","text":{"format":{"type":"json_schema","schema":{"type":"object"}}}},"environment":{"type":"none"}}`)
	for _, kind := range []string{"codex", "claude_sdk", "mcode"} {
		if err := (Policy{}).ValidateSessionConfiguration(kind, raw); (err == nil) != (kind == "claude_sdk") {
			t.Fatalf("%s: %v", kind, err)
		}
	}
	for _, qualified := range []bool{false, true} {
		policy := Policy{Engines: engine.NewCatalog(map[string]engine.Profile{"new_harness": {Placements: []string{"none"}, StructuredOutput: qualified}})}
		if err := policy.ValidateSessionConfiguration("new_harness", raw); (err == nil) != qualified {
			t.Fatal("new harness did not use common qualification", err)
		}
	}
}

func TestStructuredOutputRequestKeepsFrozenSchemaAndInstructions(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"number":{"const":9007199254740992}}}`)
	instructions := "Keep these original instructions."
	snapshot := Snapshot{Agent: v1.Agent{Model: "model", Instructions: &instructions, Text: v1.TextConfig{Format: v1.TextFormat{Type: "json_schema", Schema: schema}}}}
	request, err := (&Dispatcher{}).executionRequest(context.Background(), store.Session{}, snapshot, device.KindCapabilities{MessageItems: true}, store.SessionExecutionBinding{})
	if err != nil || request.ExecutionControls.OutputFormat == nil {
		t.Fatal(err)
	}
	format := request.ExecutionControls.OutputFormat
	if format.Type != "json_schema" || string(format.Schema) != string(schema) || request.AgentOptions["system_prompt"] != &instructions {
		t.Fatal("configuration was rewritten")
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(encoded) {
		t.Fatal("invalid request")
	}
}
