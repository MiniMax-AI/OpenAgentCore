// Package mcode declares the MiniMax Code Harness.
package mcode

import (
	"encoding/json"
	"errors"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig"
)

// FunctionServerLabel is the native MCP server that carries public functions.
const FunctionServerLabel = "functions"

// Declaration is the only authored description of the MiniMax Code Harness. The
// Runtime may narrow Capabilities for the installed host, never widen them.
// Whitespace-only text is unsupported because the native runtime refuses such
// prompts with "Local message content or attachments are required.".
var Declaration = harnessconfig.Declaration{
	Kind:   "mcode",
	Label:  "MiniMax Code",
	Native: harnessconfig.NativePackage{Component: "minimax", Version: "0.4.12"},
	Configuration: harnessconfig.Configuration{Providers: []harnessconfig.Provider{
		{Protocol: "anthropic"},
		{Protocol: "responses"},
		{Protocol: "chat_completions"},
	}, MaxOutputTokens: true},
	Capabilities: proto.AgentKindCapabilities{
		SubagentObservations:     proto.CapabilitySupported,
		Streaming:                proto.CapabilitySupported,
		Usage:                    proto.CapabilityUnsupported,
		Resume:                   proto.CapabilitySupported,
		RetainedNativeHistory:    proto.CapabilityUnsupported,
		NativeSessionRecovery:    proto.CapabilityUnsupported,
		MessageItems:             proto.CapabilityUnsupported,
		ToolObservations:         proto.CapabilitySupported,
		EnvironmentNone:          proto.CapabilitySupported,
		LocalEnvironment:         proto.CapabilitySupported,
		Preparation:              proto.CapabilitySupported,
		WorkspaceReadPreparation: proto.CapabilitySupported,
		WorkspaceOutputExport:    proto.CapabilitySupported,
		ExecutionControls:        proto.CapabilitySupported,
		StructuredOutput:         proto.CapabilityUnsupported,
		ToolSearch:               proto.CapabilityUnsupported,
		MessageImages:            proto.CapabilityUnsupported,
		FunctionResultImages:     proto.CapabilityUnsupported,
		SubagentControl:          proto.CapabilitySupported,
		DurableInputReceipts:     proto.CapabilitySupported,
		DurableTurns:             proto.CapabilitySupported,
		FunctionTools:            proto.CapabilitySupported,
		MCPHTTPTools:             proto.CapabilitySupported,
		MCPHTTPRequired:          proto.CapabilityUnsupported,
		MCPHTTPBearerAuth:        proto.CapabilitySupported,
	},
	// Native search needs a MiniMax account credential that Core does not hold, so
	// features.webSearch stays false and disabled is the only mode. Effort becomes the
	// custom model's only thinking option, and none disables native thinking.
	// MiniMax Code has no native home for summary, service tier or programmatic
	// tool calling.
	// Medium is the official default verbosity and needs no setting.
	Execution: harnessconfig.ExecutionOptions{
		WebSearchModes:          []string{"disabled"},
		ReasoningEfforts:        []string{"none", "minimal", "low", "medium", "high", "xhigh", "max"},
		ProgrammaticToolCalling: []bool{false},
		TextVerbosities:         []string{"medium"},
	},
	Placements:         []string{"none", "openai_hosted", "self_hosted"},
	MCPOrigins:         []string{"service", "environment"},
	WhitespaceOnlyText: proto.CapabilityUnsupported,
	// Native MiniMax Code ignores global instructions (AGENTS.md) over 32 KiB.
	MaxInstructionsBytes: 32 * 1024,
	ValidateTools: func(functions []proto.FunctionTool, mcp []proto.MCPHTTPServer) error {
		if err := harnessconfig.RequireObjectSchemas(functions); err != nil {
			return err
		}
		for _, tool := range functions {
			// The adapter's MCP server library gives x-mcp-header transport meaning.
			if hasKey(tool.Parameters, "x-mcp-header") {
				return errors.New("The configured engine does not support the x-mcp-header keyword in function schemas.")
			}
		}
		for _, server := range mcp {
			// Native starts session stdio servers in its home directory, which is private state, never the cwd a declaration names.
			if server.Stdio != nil {
				return errors.New("The configured engine cannot start MCP stdio servers in the declared cwd.")
			}
			if server.AllowedTools != nil || server.Required {
				return errors.New("The configured engine requires allowed_tools=null and required=false.")
			}
			// The adapter serves public functions under this label.
			if len(functions) > 0 && server.ServerLabel == FunctionServerLabel {
				return errors.New("The configured engine reserves the HTTP MCP label functions.")
			}
		}
		return nil
	},
}

// hasKey reports whether the JSON document has an object member with this name at any depth.
func hasKey(raw json.RawMessage, key string) bool {
	var found func(any) bool
	found = func(v any) bool {
		switch v := v.(type) {
		case map[string]any:
			if _, ok := v[key]; ok {
				return true
			}
			for _, child := range v {
				if found(child) {
					return true
				}
			}
		case []any:
			for _, child := range v {
				if found(child) {
					return true
				}
			}
		}
		return false
	}
	var doc any
	return json.Unmarshal(raw, &doc) == nil && found(doc)
}
