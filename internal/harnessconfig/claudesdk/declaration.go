// Package claudesdk declares the Claude Code Harness.
package claudesdk

import (
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig"
)

// Declaration is the only authored description of the Claude Code Harness. The
// Runtime may narrow Capabilities to the installed bundle, never widen them.
// Whitespace-only text is unsupported because the bridge and Anthropic-compatible
// providers reject text without non-whitespace characters.
var Declaration = harnessconfig.Declaration{
	Kind:  "claude_sdk",
	Label: "Claude Code",
	Configuration: harnessconfig.Configuration{
		ValidateNativeConfig: validateNativeConfig,
		Providers:            []harnessconfig.Provider{{Protocol: "anthropic"}},
	},
	Capabilities: proto.AgentKindCapabilities{
		SubagentObservations:     proto.CapabilitySupported,
		Streaming:                proto.CapabilitySupported,
		Usage:                    proto.CapabilitySupported,
		Resume:                   proto.CapabilitySupported,
		RetainedNativeHistory:    proto.CapabilitySupported,
		NativeSessionRecovery:    proto.CapabilitySupported,
		Steering:                 proto.CapabilitySupported,
		MessageItems:             proto.CapabilitySupported,
		ToolObservations:         proto.CapabilitySupported,
		EnvironmentNone:          proto.CapabilitySupported,
		LocalEnvironment:         proto.CapabilitySupported,
		Preparation:              proto.CapabilitySupported,
		WorkspaceReadPreparation: proto.CapabilitySupported,
		WorkspaceOutputExport:    proto.CapabilitySupported,
		ExecutionControls:        proto.CapabilitySupported,
		StructuredOutput:         proto.CapabilitySupported,
		ToolSearch:               proto.CapabilitySupported,
		MessageImages:            proto.CapabilitySupported,
		FunctionResultImages:     proto.CapabilitySupported,
		SubagentControl:          proto.CapabilitySupported,
		DurableInputReceipts:     proto.CapabilitySupported,
		DurableTurns:             proto.CapabilitySupported,
		FunctionTools:            proto.CapabilitySupported,
		MCPHTTPTools:             proto.CapabilitySupported,
		MCPHTTPRequired:          proto.CapabilitySupported,
		MCPHTTPBearerAuth:        proto.CapabilitySupported,
	},
	// The SDK takes effort low to max. It has no verbosity, summary, service
	// tier, search mode or search setting, and enabling programmatic tool calling
	// is not wired. Search is off in the fixed tool inventory, so disabled is the
	// only mode; medium is the official default verbosity and needs no setting.
	Execution: harnessconfig.ExecutionOptions{
		WebSearchModes:          []string{"disabled"},
		ReasoningEfforts:        []string{"low", "medium", "high", "xhigh", "max"},
		ProgrammaticToolCalling: []bool{false},
		TextVerbosities:         []string{"medium"},
	},
	Placements:             []string{"none", "openai_hosted", "self_hosted"},
	MCPOrigins:             []string{"service", "environment"},
	WhitespaceOnlyText:     proto.CapabilityUnsupported,
	ValidateConfiguration:  validateConfiguration,
	ValidateTools:          validateTools,
	ValidateFunctionResult: validateFunctionResult,
}
