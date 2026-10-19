// Package mcode declares the MiniMax Code Harness.
package mcode

import (
	"errors"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig"
)

// Declaration is the only authored description of the MiniMax Code Harness. The
// Runtime may narrow Capabilities for the installed host, never widen them.
// Whitespace-only text is unsupported because the native runtime refuses such
// prompts with "Local message content or attachments are required.".
var Declaration = harnessconfig.Declaration{
	Kind:  "mcode",
	Label: "MiniMax Code",
	Configuration: harnessconfig.Configuration{Providers: []harnessconfig.Provider{
		{Protocol: "anthropic"},
		{Protocol: "responses"},
		{Protocol: "chat_completions"},
	}},
	Capabilities: proto.AgentKindCapabilities{
		SubagentObservations:     proto.CapabilitySupported,
		Streaming:                proto.CapabilitySupported,
		Usage:                    proto.CapabilityUnsupported,
		Resume:                   proto.CapabilitySupported,
		RetainedNativeHistory:    proto.CapabilityUnsupported,
		NativeSessionRecovery:    proto.CapabilityUnsupported,
		Steering:                 proto.CapabilitySupported,
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
		FunctionTools:            proto.CapabilityUnsupported,
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
	ValidateConfiguration: func(admission harnessconfig.Admission) error {
		return harnessconfig.RejectSubagentTools(admission, "mcp")
	},
	ValidateTools: func(functions []proto.FunctionTool, mcp []proto.MCPHTTPServer) error {
		if len(functions) > 0 {
			return errors.New("The configured engine does not support public functions.")
		}
		for _, server := range mcp {
			if server.AllowedTools != nil || server.Required {
				return errors.New("The configured engine requires allowed_tools=null and required=false.")
			}
		}
		return nil
	},
}
