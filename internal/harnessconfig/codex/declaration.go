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
		Providers: []harnessconfig.Provider{{Protocol: "responses"}},
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
		StructuredOutput:         proto.CapabilityUnsupported,
		ToolSearch:               proto.CapabilityUnsupported,
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
	// Codex maps every official value natively: web_search, tools.web_search.*,
	// model_reasoning_effort, model_reasoning_summary, service_tier,
	// model_verbosity and features.code_mode. Native indexed search and the
	// legacy fast tier have no official value. Official auto service tier means
	// unset. Codex ignores a value the selected model does not support.
	Execution: harnessconfig.ExecutionOptions{
		WebSearchModes:          []string{"disabled", "cached", "live"},
		WebSearchSettings:       true,
		ReasoningEfforts:        []string{"none", "minimal", "low", "medium", "high", "xhigh", "max"},
		ReasoningSummaries:      []string{"concise", "detailed", "auto"},
		ServiceTiers:            []string{"default", "flex", "priority"},
		ProgrammaticToolCalling: []bool{true, false},
		TextVerbosities:         []string{"low", "medium", "high"},
	},
	Placements:         []string{"none", "self_hosted", "openai_hosted"},
	MCPOrigins:         []string{"service", "environment"},
	WhitespaceOnlyText: proto.CapabilitySupported,
	ValidateConfiguration: func(admission harnessconfig.Admission) error {
		return harnessconfig.RejectSubagentTools(admission, "function", "mcp")
	},
}
