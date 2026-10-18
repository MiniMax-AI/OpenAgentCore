// Package codex declares the Codex Harness.
package codex

import (
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig"
)

// Declaration is the only authored description of the Codex Harness. The
// Runtime may narrow Capabilities for the installed host, never widen them.
var Declaration = harnessconfig.Declaration{
	Kind:  "codex",
	Label: "Codex",
	Configuration: harnessconfig.Configuration{
		ValidateNativeConfig: validateNativeConfig,
		Providers:            []harnessconfig.Provider{{Protocol: "responses"}},
	},
	Capabilities: proto.AgentKindCapabilities{
		SubagentObservations:           proto.CapabilitySupported,
		Streaming:                      proto.CapabilitySupported,
		Usage:                          proto.CapabilitySupported,
		Resume:                         proto.CapabilitySupported,
		RetainedNativeHistory:          proto.CapabilitySupported,
		NativeSessionRecovery:          proto.CapabilitySupported,
		Steering:                       proto.CapabilitySupported,
		MessageItems:                   proto.CapabilitySupported,
		ToolObservations:               proto.CapabilitySupported,
		EnvironmentNone:                proto.CapabilitySupported,
		LocalEnvironment:               proto.CapabilitySupported,
		Preparation:                    proto.CapabilitySupported,
		WorkspaceReadPreparation:       proto.CapabilitySupported,
		WorkspaceOutputExport:          proto.CapabilitySupported,
		ProgrammaticToolCallingDisable: proto.CapabilitySupported,
		WebSearchControl:               proto.CapabilitySupported,
		ExecutionControls:              proto.CapabilitySupported,
		TextVerbosity:                  proto.CapabilitySupported,
		StructuredOutput:               proto.CapabilityUnsupported,
		ToolSearch:                     proto.CapabilityUnsupported,
		MessageImages:                  proto.CapabilitySupported,
		FunctionResultImages:           proto.CapabilitySupported,
		SubagentControl:                proto.CapabilitySupported,
		DurableInputReceipts:           proto.CapabilitySupported,
		DurableTurns:                   proto.CapabilitySupported,
		FunctionTools:                  proto.CapabilitySupported,
		MCPHTTPTools:                   proto.CapabilitySupported,
		MCPHTTPRequired:                proto.CapabilitySupported,
		MCPHTTPBearerAuth:              proto.CapabilitySupported,
	},
	Placements:         []string{"none", "self_hosted", "openai_hosted"},
	MCPOrigins:         []string{"service", "environment"},
	WhitespaceOnlyText: proto.CapabilitySupported,
	ValidateConfiguration: func(admission harnessconfig.Admission) error {
		return harnessconfig.RejectSubagentTools(admission, "function", "mcp")
	},
}
