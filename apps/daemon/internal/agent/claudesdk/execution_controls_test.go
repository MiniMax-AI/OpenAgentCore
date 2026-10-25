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
	root := t.TempDir()
	t.Setenv("OAC_RUNTIME_HOME", root)
	config := Config{Entrypoint: filepath.Join(root, "worker"), StateDir: filepath.Join(root, "state")}
	request := proto.PromptRequestPayload{RunID: "run", Input: proto.TextInput("Original input."), AgentSessionID: "native-session", AgentOptions: map[string]any{"model": "native-model", "system_prompt": "Keep these exact instructions.\nDo not replace them."}}
	ordinary, _, err := prepareConfiguration(config, request)
	if err != nil {
		t.Fatal(err)
	}
	request.ExecutionControls = &proto.ExecutionControls{WebSearch: &proto.WebSearchControl{Mode: "disabled", ContextSize: "medium"}, TextVerbosity: "medium"}
	before, _ := json.Marshal(request)
	controlled, _, err := prepareConfiguration(config, request)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(request)
	if controlled.WebSearch != "disabled" || ordinary.WebSearch != "" {
		t.Fatal("disabled web search did not reach the bridge")
	}
	controlled.WebSearch = ""
	if !reflect.DeepEqual(ordinary, controlled) || string(before) != string(after) {
		t.Fatal("default controls changed native input, instructions, continuation or caller options")
	}
	// Without a search control Claude Code keeps its native web tools.
	request.ExecutionControls = &proto.ExecutionControls{TextVerbosity: "medium"}
	absent, _, err := prepareConfiguration(config, request)
	if err != nil || !reflect.DeepEqual(ordinary, absent) {
		t.Fatal("absent search control changed the native configuration", err)
	}
}

func TestLiveWebSearchReachesTheBridgeOnEveryPlacement(t *testing.T) {
	live := &proto.ExecutionControls{WebSearch: &proto.WebSearchControl{Mode: "live", ContextSize: "medium"}, TextVerbosity: "medium"}
	root := t.TempDir()
	t.Setenv("OAC_RUNTIME_HOME", root)
	none, _, err := prepareConfiguration(Config{Entrypoint: filepath.Join(root, "worker"), StateDir: filepath.Join(root, "state")}, proto.PromptRequestPayload{
		RunID: "run", Input: proto.TextInput("Search."), ExecutionControls: live, AgentOptions: map[string]any{"model": "native-model"}})
	if err != nil || none.WebSearch != "live" {
		t.Fatal("live search on environment none did not reach the bridge", err)
	}
	request := workspaceRequest()
	request.ExecutionControls = live
	workspace, _, err := prepareConfiguration(workspaceFixture(t), request)
	if err != nil || workspace.WebSearch != "live" {
		t.Fatal("live search on a workspace did not reach the bridge", err)
	}
}

func TestExecutionControlsRejectUnsupportedProfilesBeforeLaunch(t *testing.T) {
	enabled := true
	cases := map[string]proto.ExecutionControls{
		"cached-search":     {WebSearch: &proto.WebSearchControl{Mode: "cached", ContextSize: "medium"}},
		"enabled-ptc":       {ProgrammaticToolCalling: &enabled},
		"low-verbosity":     {TextVerbosity: "low"},
		"high-verbosity":    {TextVerbosity: "high"},
		"unknown-verbosity": {TextVerbosity: "invalid"},
		"no-effort":         {ReasoningEffort: "none"},
		"minimal-effort":    {ReasoningEffort: "minimal"},
		"summary":           {ReasoningSummary: "concise"},
		"service-tier":      {ServiceTier: "priority"},
	}
	for name, controls := range cases {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("OAC_RUNTIME_HOME", root)
			config := Config{Node: "must-not-run", Entrypoint: filepath.Join(root, "worker"), StateDir: filepath.Join(root, "state")}
			request := proto.PromptRequestPayload{RunID: "run", Input: proto.TextInput("Original input."), ExecutionControls: &controls, AgentOptions: map[string]any{"model": "native-model"}}
			_, err := startSingleTurn(t.Context(), config, request, make(chan proto.Envelope, 1))
			if err == nil || !strings.Contains(err.Error(), "not supported by this Harness") {
				t.Fatal("unsupported controls did not fail at admission", err)
			}
			if _, err := os.Stat(config.StateDir); !os.IsNotExist(err) {
				t.Fatal("unsupported controls reached native setup", err)
			}
		})
	}
}

func TestReasoningEffortReachesNativeOptions(t *testing.T) {
	root := t.TempDir()
	t.Setenv("OAC_RUNTIME_HOME", root)
	config := Config{Entrypoint: filepath.Join(root, "worker"), StateDir: filepath.Join(root, "state")}
	for _, effort := range []string{"low", "medium", "high", "xhigh", "max"} {
		request := proto.PromptRequestPayload{RunID: "run", Input: proto.TextInput("hello"), ExecutionControls: &proto.ExecutionControls{TextVerbosity: "medium", ReasoningEffort: effort}, AgentOptions: map[string]any{"model": "native-model", "harness_config": map[string]any{"thinking": map[string]any{"type": "adaptive"}}}}
		start, _, err := prepareConfiguration(config, request)
		if err != nil {
			t.Fatal(effort, err)
		}
		if start.NativeModelOptions == nil || start.NativeModelOptions.Effort != effort || start.NativeModelOptions.Thinking["type"] != "adaptive" {
			t.Fatalf("effort %s: %+v", effort, start.NativeModelOptions)
		}
	}
}

func TestMCPWithoutEnvironmentNoneRejectedBeforeSetup(t *testing.T) {
	root := t.TempDir()
	t.Setenv("OAC_RUNTIME_HOME", root)
	config := Config{Node: "must-not-run", Entrypoint: filepath.Join(root, "worker"), StateDir: filepath.Join(root, "state")}
	servers := []proto.MCPHTTPServer{{ConnectionOrigin: "service", ServerLabel: "remote", ServerURL: "https://example.test/mcp"}}
	request := proto.PromptRequestPayload{RunID: "run", Input: proto.TextInput("Input"), MCPHTTPServers: &servers}
	_, err := startSingleTurn(t.Context(), config, request, make(chan proto.Envelope, 1))
	if err == nil || !strings.Contains(err.Error(), "service-origin MCP requires a service execution host") {
		t.Fatal("MCP reached an unsupported environment", err)
	}
	if _, err := os.Stat(config.StateDir); !os.IsNotExist(err) {
		t.Fatal("MCP reached native setup", err)
	}
}

func TestStructuredOutputConfigurationReachesNativeUnchanged(t *testing.T) {
	root := t.TempDir()
	t.Setenv("OAC_RUNTIME_HOME", root)
	config := Config{Entrypoint: filepath.Join(root, "worker"), StateDir: filepath.Join(root, "state")}
	schema := json.RawMessage(`{"type":"object","properties":{"n":{"const":9007199254740992}}}`)
	request := proto.PromptRequestPayload{RunID: "run", Input: proto.TextInput("Original input."), DisableSubagents: true, AgentOptions: map[string]any{"model": "model", "system_prompt": "Original instructions."}, ExecutionControls: &proto.ExecutionControls{TextVerbosity: "medium", OutputFormat: &proto.OutputFormat{Type: "json_schema", Schema: schema}}}
	start, _, err := prepareConfiguration(config, request)
	if err != nil {
		t.Fatal(err)
	}
	if start.OutputFormat == nil || string(start.OutputFormat.Schema) != string(schema) || start.SystemPrompt != "Original instructions." {
		t.Fatal("native configuration changed")
	}
	request.ExecutionControls.OutputFormat.Schema = json.RawMessage(`{"type":"object","const":9007199254740993}`)
	if _, _, err := prepareConfiguration(config, request); err == nil {
		t.Fatal("lossy schema accepted")
	}
	request.ExecutionControls.OutputFormat.Schema = schema
	request.DisableExecutionEnvironment = true
	request.MCPHTTPServers = &[]proto.MCPHTTPServer{{ConnectionOrigin: "service", ServerLabel: "fixture", ServerURL: "https://example.invalid/mcp"}}
	if start, _, err := prepareConfiguration(config, request); err != nil || start.OutputFormat == nil {
		t.Fatal("native structured output was narrowed by MCP", err)
	}
}

func TestToolDiscoveryPreservesFrozenFunctionsBesideOtherProfiles(t *testing.T) {
	root := t.TempDir()
	t.Setenv("OAC_RUNTIME_HOME", root)
	config := Config{Entrypoint: filepath.Join(root, "worker"), StateDir: filepath.Join(root, "state")}
	request := proto.PromptRequestPayload{RunID: "run", Input: proto.TextInput("Original input."), DisableSubagents: true, ToolSearch: true, AgentOptions: map[string]any{"model": "model"}, FunctionTools: []proto.FunctionTool{
		{Name: "lookup", Description: "Lookup", Parameters: json.RawMessage(`{"type":"object","properties":{"ticket":{"const":"original"}}}`), DeferLoading: true},
		{Name: "clock", Description: "Clock", Parameters: json.RawMessage(`{"type":"object"}`)},
	}}
	start, _, err := prepareConfiguration(config, request)
	if err != nil || !start.ToolSearch || !reflect.DeepEqual(start.Functions, request.FunctionTools) {
		t.Fatal("function discovery changed native definitions", err)
	}
	request.ExecutionControls = &proto.ExecutionControls{TextVerbosity: "medium", OutputFormat: &proto.OutputFormat{Type: "json_schema", Schema: json.RawMessage(`{"type":"object"}`)}}
	if start, _, err := prepareConfiguration(config, request); err != nil || !start.ToolSearch || start.OutputFormat == nil {
		t.Fatal("native tool search was narrowed by structured output", err)
	}
	request.ExecutionControls = nil
	request.ToolSearch = false
	if _, _, err := prepareConfiguration(config, request); err == nil {
		t.Fatal("deferred definitions became eager")
	}
}

func TestReasoningSummaryReachesTheBridgeOnlyWhenRequested(t *testing.T) {
	root := t.TempDir()
	t.Setenv("OAC_RUNTIME_HOME", root)
	config := Config{Entrypoint: filepath.Join(root, "worker"), StateDir: filepath.Join(root, "state")}
	request := proto.PromptRequestPayload{RunID: "run", Input: proto.TextInput("Input."), AgentOptions: map[string]any{"model": "native-model"}}
	for _, summary := range []string{"", "auto"} {
		request.ExecutionControls = &proto.ExecutionControls{TextVerbosity: "medium", ReasoningSummary: summary}
		start, _, err := prepareConfiguration(config, request)
		if err != nil || start.ReasoningSummary != (summary != "") {
			t.Fatalf("summary %q: reasoning flag %v, err %v", summary, start.ReasoningSummary, err)
		}
	}
}
