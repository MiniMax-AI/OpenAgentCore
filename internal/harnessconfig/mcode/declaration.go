// Package mcode declares the MiniMax Code Harness.
package mcode

import (
	"errors"
	"strings"

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
		{Protocol: "anthropic", RequiresTokenLimits: true},
		{Protocol: "responses", RequiresTokenLimits: true},
		{Protocol: "chat_completions", RequiresTokenLimits: true},
	}},
	Capabilities: proto.AgentKindCapabilities{
		SubagentObservations:           proto.CapabilitySupported,
		Streaming:                      proto.CapabilitySupported,
		Usage:                          proto.CapabilityUnsupported,
		Resume:                         proto.CapabilitySupported,
		RetainedNativeHistory:          proto.CapabilityUnsupported,
		NativeSessionRecovery:          proto.CapabilityUnsupported,
		Steering:                       proto.CapabilitySupported,
		MessageItems:                   proto.CapabilityUnsupported,
		ToolObservations:               proto.CapabilitySupported,
		EnvironmentNone:                proto.CapabilitySupported,
		LocalEnvironment:               proto.CapabilitySupported,
		Preparation:                    proto.CapabilitySupported,
		WorkspaceReadPreparation:       proto.CapabilitySupported,
		WorkspaceOutputExport:          proto.CapabilitySupported,
		ProgrammaticToolCallingDisable: proto.CapabilitySupported,
		WebSearchControl:               proto.CapabilityUnsupported,
		ExecutionControls:              proto.CapabilitySupported,
		TextVerbosity:                  proto.CapabilityUnsupported,
		StructuredOutput:               proto.CapabilityUnsupported,
		ToolSearch:                     proto.CapabilityUnsupported,
		MessageImages:                  proto.CapabilityUnsupported,
		FunctionResultImages:           proto.CapabilityUnsupported,
		SubagentControl:                proto.CapabilitySupported,
		DurableInputReceipts:           proto.CapabilitySupported,
		DurableTurns:                   proto.CapabilitySupported,
		FunctionTools:                  proto.CapabilityUnsupported,
		MCPHTTPTools:                   proto.CapabilitySupported,
		MCPHTTPRequired:                proto.CapabilityUnsupported,
		MCPHTTPBearerAuth:              proto.CapabilitySupported,
	},
	Placements:         []string{"none", "openai_hosted", "self_hosted"},
	MCPOrigins:         []string{"environment"},
	WhitespaceOnlyText: proto.CapabilityUnsupported,
	ValidateConfiguration: func(admission harnessconfig.Admission) error {
		environment := admission.Environment
		if environment == nil || (environment.Type != "none" && environment.Type != "openai_hosted" && environment.Type != "self_hosted") ||
			strings.TrimSpace(admission.Model) == "" || admission.Reasoning || (admission.ServiceTier != "" && admission.ServiceTier != "auto") ||
			(admission.TextFormat != "" && admission.TextFormat != "text") || (admission.TextVerbosity != "" && admission.TextVerbosity != "medium") {
			return harnessconfig.ErrInvalidInput
		}
		return harnessconfig.RejectSubagentTools(admission, "mcp")
	},
	ValidateTools: func(functions []proto.FunctionTool, mcp []proto.MCPHTTPServer) error {
		if len(functions) > 0 {
			return errors.New("The configured engine does not support public functions.")
		}
		for _, server := range mcp {
			if server.ServerLabel == "oac_workspace" || server.AllowedTools != nil || server.Required {
				return errors.New("The configured engine requires an unreserved MCP label, allowed_tools=null and required=false.")
			}
		}
		return nil
	},
}
