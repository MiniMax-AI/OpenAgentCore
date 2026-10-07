package execution

import (
	"context"
	"encoding/json"
	"testing"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

func TestStructuredOutputRequestKeepsFrozenSchemaAndInstructions(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"number":{"const":9007199254740992}}}`)
	instructions := "Keep these original instructions."
	snapshot := Snapshot{ModelProviderConfigured: true, Agent: v1.Agent{Model: "model", Instructions: &instructions, Text: v1.TextConfig{Format: v1.TextFormat{Type: "json_schema", Schema: schema}}}}
	request, err := (&Dispatcher{SessionsReader: frozenProvider{engine: "claude_sdk"}}).executionRequest(context.Background(), sessions.Session{Engine: "claude_sdk"}, snapshot, proto.Declaration{}, sessions.ExecutionBinding{})
	if err != nil || request.ExecutionControls.OutputFormat == nil {
		t.Fatal(err)
	}
	format := request.ExecutionControls.OutputFormat
	if format.Type != "json_schema" || string(format.Schema) != string(schema) || request.SystemPrompt != instructions {
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
