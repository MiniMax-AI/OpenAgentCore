package cli

import (
	"context"
	"fmt"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent/mcode"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func discoverMCode(parent context.Context, rc *runContext, check func(context.Context, string) (string, error)) proto.SupportedAgentKind {
	if check == nil {
		check = mcode.CheckCLIAvailable
	}
	result := proto.SupportedAgentKind{Kind: "mcode", Capabilities: proto.AgentKindCapabilities{
		SubagentObservations:           proto.CapabilityUnsupported,
		Streaming:                      proto.CapabilitySupported,
		Permissions:                    proto.CapabilitySupported,
		Usage:                          proto.CapabilityUnsupported,
		Resume:                         proto.CapabilitySupported,
		NativeSessionRecovery:          proto.CapabilityUnsupported,
		WorkspaceAuthoring:             proto.CapabilityUnsupported,
		Steering:                       proto.CapabilityUnsupported,
		MessageItems:                   proto.CapabilityUnsupported,
		ToolObservations:               proto.CapabilityUnsupported,
		EnvironmentNone:                proto.CapabilityUnsupported,
		LocalEnvironment:               proto.CapabilityUnsupported,
		Preparation:                    proto.CapabilityUnsupported,
		WorkspaceReadPreparation:       proto.CapabilityUnsupported,
		WorkspaceOutputExport:          proto.CapabilityUnsupported,
		ProgrammaticToolCallingDisable: proto.CapabilityUnsupported,
		WebSearchControl:               proto.CapabilityUnsupported,
		ExecutionControls:              proto.CapabilityUnsupported,
		TextVerbosity:                  proto.CapabilityUnsupported,
		StructuredOutput:               proto.CapabilityUnsupported,
		ToolSearch:                     proto.CapabilityUnsupported,
		MessageImages:                  proto.CapabilityUnsupported,
		FunctionResultImages:           proto.CapabilityUnsupported,
		SubagentControl:                proto.CapabilityUnsupported,
		DurableInputReceipts:           proto.CapabilityUnsupported,
		DurableTurns:                   proto.CapabilityUnsupported,
		FunctionTools:                  proto.CapabilityUnsupported,
		MCPHTTPTools:                   proto.CapabilityUnsupported,
		MCPHTTPRequired:                proto.CapabilityUnsupported,
		MCPHTTPBearerAuth:              proto.CapabilityUnsupported,
	}}
	ctx, cancel := context.WithTimeout(parent, cliVersionTimeout)
	defer cancel()
	version, err := check(ctx, "")
	if err != nil {
		fmt.Fprintf(rc.stderr, "oac-daemon: mcode unavailable: %v\n  Install: npm install -g @minimax-ai/code@0.4.12\n", err)
		return result
	}
	result.Available, result.Version = true, version
	if mcode.SupportsExecution(version) {
		result.Capabilities.Steering = proto.CapabilitySupported
		result.Capabilities.DurableTurns = proto.CapabilitySupported
		result.Capabilities.DurableInputReceipts = proto.CapabilitySupported
		result.Capabilities.ExecutionControls = proto.CapabilitySupported
		result.Capabilities.ProgrammaticToolCallingDisable = proto.CapabilitySupported
		result.Capabilities.ToolObservations = proto.CapabilitySupported
		result.Capabilities.SubagentControl = proto.CapabilitySupported
		// Native preparation verifies the applied admission/tool profile before input.
		result.Capabilities.SubagentObservations = proto.CapabilitySupported
		result.Capabilities.EnvironmentNone = proto.CapabilitySupported
		result.Capabilities.MCPHTTPTools = proto.CapabilitySupported
		result.Capabilities.MCPHTTPBearerAuth = proto.CapabilitySupported
	}
	fmt.Fprintf(rc.stdout, "mcode preflight ok (%s)\n", version)
	return result
}
