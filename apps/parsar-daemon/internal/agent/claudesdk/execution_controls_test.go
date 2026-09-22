//go:build unix

package claudesdk

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func TestExecutionControlsPreserveNativeDefaultsAndInstructions(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PARSAR_HOME", root)
	config := Config{Entrypoint: filepath.Join(root, "worker"), StateDir: filepath.Join(root, "state")}
	request := proto.PromptRequestPayload{RunID: "run", Input: proto.TextInput("Original input."), AgentSessionID: "native-session", AgentOptions: map[string]any{"model": "native-model", "system_prompt": "Keep these exact instructions.\nDo not replace them."}}
	ordinary, _, err := prepare(config, request)
	if err != nil {
		t.Fatal(err)
	}
	request.ExecutionControls = &proto.ExecutionControls{WebSearch: "disabled", TextVerbosity: "medium"}
	before, _ := json.Marshal(request)
	controlled, _, err := prepare(config, request)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(request)
	if !reflect.DeepEqual(ordinary, controlled) || string(before) != string(after) {
		t.Fatal("default controls changed native input, instructions, continuation or caller options")
	}
}

func TestExecutionControlsRejectUnsupportedProfilesBeforeLaunch(t *testing.T) {
	cases := map[string]proto.ExecutionControls{
		"empty": {}, "missing-search": {TextVerbosity: "medium"}, "missing-verbosity": {WebSearch: "disabled"},
		"cached-search":     {WebSearch: "cached", TextVerbosity: "medium"},
		"live-search":       {WebSearch: "live", TextVerbosity: "medium"},
		"unknown-search":    {WebSearch: "invalid", TextVerbosity: "medium"},
		"low-verbosity":     {WebSearch: "disabled", TextVerbosity: "low"},
		"high-verbosity":    {WebSearch: "disabled", TextVerbosity: "high"},
		"unknown-verbosity": {WebSearch: "disabled", TextVerbosity: "invalid"},
	}
	for name, controls := range cases {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("PARSAR_HOME", root)
			config := Config{Node: "must-not-run", Entrypoint: filepath.Join(root, "worker"), StateDir: filepath.Join(root, "state")}
			request := proto.PromptRequestPayload{RunID: "run", Input: proto.TextInput("Original input."), ExecutionControls: &controls, AgentOptions: map[string]any{"model": "native-model"}}
			_, err := NewFactory(config)(t.Context(), request, make(chan proto.Envelope, 1))
			if err == nil || !strings.Contains(err.Error(), "execution controls require") {
				t.Fatal("unsupported controls did not fail at admission", err)
			}
			if _, err := os.Stat(config.StateDir); !os.IsNotExist(err) {
				t.Fatal("unsupported controls reached native setup", err)
			}
		})
	}
}

func TestMCPWithoutEnvironmentNoneRejectedBeforeSetup(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PARSAR_HOME", root)
	config := Config{Node: "must-not-run", Entrypoint: filepath.Join(root, "worker"), StateDir: filepath.Join(root, "state")}
	servers := []proto.MCPHTTPServer{}
	request := proto.PromptRequestPayload{RunID: "run", Input: proto.TextInput("Input"), MCPHTTPServers: &servers}
	_, err := NewFactory(config)(t.Context(), request, make(chan proto.Envelope, 1))
	if err == nil || !strings.Contains(err.Error(), "HTTP MCP requires environment:none") {
		t.Fatal("MCP reached an unsupported environment", err)
	}
	if _, err := os.Stat(config.StateDir); !os.IsNotExist(err) {
		t.Fatal("MCP reached native setup", err)
	}
}

func TestStructuredOutputConfigurationReachesNativeUnchanged(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PARSAR_HOME", root)
	config := Config{Entrypoint: filepath.Join(root, "worker"), StateDir: filepath.Join(root, "state")}
	schema := json.RawMessage(`{"type":"object","properties":{"n":{"const":9007199254740992}}}`)
	request := proto.PromptRequestPayload{RunID: "run", Input: proto.TextInput("Original input."), ObserveMessages: true, DisableSubagents: true, AgentOptions: map[string]any{"model": "model", "system_prompt": "Original instructions."}, ExecutionControls: &proto.ExecutionControls{WebSearch: "disabled", TextVerbosity: "medium", OutputFormat: &proto.OutputFormat{Type: "json_schema", Schema: schema}}}
	start, _, err := prepare(config, request)
	if err != nil {
		t.Fatal(err)
	}
	if start.OutputFormat == nil || string(start.OutputFormat.Schema) != string(schema) || *start.Input[0].Content[0].Text != *request.Input[0].Content[0].Text || start.SystemPrompt != "Original instructions." {
		t.Fatal("native configuration changed")
	}
	request.ExecutionControls.OutputFormat.Schema = json.RawMessage(`{"type":"object","const":9007199254740993}`)
	if _, _, err := prepare(config, request); err == nil {
		t.Fatal("lossy schema accepted")
	}
	request.ExecutionControls.OutputFormat.Schema = schema
	request.DisableSubagents = false
	if _, _, err := prepare(config, request); err == nil {
		t.Fatal("unqualified subagent combination accepted")
	}
}

func TestToolDiscoveryPreservesFrozenFunctionsAndRejectsOtherProfiles(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PARSAR_HOME", root)
	config := Config{Entrypoint: filepath.Join(root, "worker"), StateDir: filepath.Join(root, "state")}
	request := proto.PromptRequestPayload{RunID: "run", Input: proto.TextInput("Original input."), DisableSubagents: true, ToolSearch: true, AgentOptions: map[string]any{"model": "model"}, FunctionTools: []proto.FunctionTool{
		{Name: "lookup", Description: "Lookup", Parameters: json.RawMessage(`{"type":"object","properties":{"ticket":{"const":"original"}}}`), DeferLoading: true},
		{Name: "clock", Description: "Clock", Parameters: json.RawMessage(`{"type":"object"}`)},
	}}
	start, _, err := prepare(config, request)
	if err != nil || !start.ToolSearch || !reflect.DeepEqual(start.Functions, request.FunctionTools) {
		t.Fatal("function discovery changed native definitions", err)
	}
	request.DisableSubagents = false
	if _, _, err := prepare(config, request); err == nil {
		t.Fatal("unqualified combination admitted")
	}
	request.DisableSubagents = true
	request.ToolSearch = false
	if _, _, err := prepare(config, request); err == nil {
		t.Fatal("deferred definitions became eager")
	}
}
