//go:build unix

package claudesdk

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

func TestExecutionControlsPreserveNativeDefaultsAndInstructions(t *testing.T) {
	request := proto.PromptRequestPayload{ModelProvider: fixtureProvider(), AgentSessionID: "native-session", Model: "native-model", SystemPrompt: "Keep these exact instructions.\nDo not replace them."}
	ordinary, _, err := prepareOptions(prepared(t, request))
	if err != nil {
		t.Fatal(err)
	}
	request.ExecutionControls = &proto.ExecutionControls{TextVerbosity: "medium"}
	before, _ := json.Marshal(request)
	controlled, _, err := prepareOptions(prepared(t, request))
	if err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(request)
	if !reflect.DeepEqual(ordinary, controlled) || string(before) != string(after) {
		t.Fatal("default controls changed instructions, continuation or caller options")
	}
}

func TestMCPWithoutEnvironmentNoneRejectedBeforeSetup(t *testing.T) {
	root := t.TempDir()
	config := testBridge{Config: Config{Node: "must-not-run", Entrypoint: filepath.Join(root, "worker")}, Home: root, Workspace: root}
	servers := []proto.MCPHTTPServer{{ConnectionOrigin: "service", ServerLabel: "remote", ServerURL: "https://example.test/mcp"}}
	request := proto.PromptRequestPayload{LocalEnvironment: &proto.LocalEnvironment{ID: "environment"}, MCPHTTPServers: &servers, Model: "fixture", ModelProvider: fixtureProvider()}
	_, err := startSingleTurn(t.Context(), config, request, "run", proto.TextInput("Input"), make(chan proto.Envelope, 1))
	if err == nil || !strings.Contains(err.Error(), "service-origin MCP requires a service execution host") {
		t.Fatal("MCP reached an unsupported environment", err)
	}
	if _, err := os.Stat(config.StateDir()); !os.IsNotExist(err) {
		t.Fatal("MCP reached native setup", err)
	}
}

func TestStructuredOutputConfigurationReachesNativeUnchanged(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"n":{"const":9007199254740992}}}`)
	request := proto.PromptRequestPayload{ModelProvider: fixtureProvider(), DisableSubagents: true, Model: "model", SystemPrompt: "Original instructions.", ExecutionControls: &proto.ExecutionControls{TextVerbosity: "medium", OutputFormat: &proto.OutputFormat{Type: "json_schema", Schema: schema}}}
	start, _, err := prepareOptions(prepared(t, request))
	if err != nil {
		t.Fatal(err)
	}
	if start.OutputFormat == nil || string(start.OutputFormat.Schema) != string(schema) || start.SystemPrompt != "Original instructions." {
		t.Fatal("native configuration changed")
	}
}

// Tool discovery keeps the frozen definitions; a typeless parameters root
// becomes an object root, which admits the same arguments.
func TestToolDiscoveryPreservesFrozenFunctions(t *testing.T) {
	request := proto.PromptRequestPayload{ModelProvider: fixtureProvider(), DisableSubagents: true, ToolSearch: true, Model: "model", FunctionTools: []proto.FunctionTool{
		{Name: "lookup", Description: "Lookup", Parameters: json.RawMessage(`{"type":"object","properties":{"ticket":{"const":"original"}}}`), DeferLoading: true},
		{Name: "clock", Description: "Clock", Parameters: json.RawMessage(`{"properties":{}}`)},
		{Name: "note", Description: "Note", Parameters: json.RawMessage(`{"type":["object","null"]}`)},
	}}
	start, _, err := prepareOptions(prepared(t, request))
	if err != nil || !start.ToolSearch || !reflect.DeepEqual(start.Functions[0], request.FunctionTools[0]) || string(start.Functions[1].Parameters) != `{"properties":{},"type":"object"}` || string(start.Functions[2].Parameters) != `{"type":"object"}` {
		t.Fatal("function discovery changed native definitions", err)
	}
	if string(request.FunctionTools[1].Parameters) != `{"properties":{}}` {
		t.Fatal("typing the root changed the frozen request")
	}
}
