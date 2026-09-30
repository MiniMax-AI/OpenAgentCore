package mcode

import (
	"context"
	"fmt"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	configuration "github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig/mcode"
)

// Declaration owns MiniMax Code discovery, configuration and execution factories.
var Declaration = agent.Declaration{Info: proto.SupportedAgentKind{Kind: "mcode", Capabilities: proto.AgentKindCapabilities{
	SubagentObservations:           proto.CapabilityUnsupported,
	Streaming:                      proto.CapabilitySupported,
	Permissions:                    proto.CapabilitySupported,
	Usage:                          proto.CapabilityUnsupported,
	Resume:                         proto.CapabilitySupported,
	NativeSessionRecovery:          proto.CapabilityUnsupported,
	WorkspaceAuthoring:             proto.CapabilitySupported,
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
}}, Configuration: configuration.Configuration(), Discover: discover}

func discover(ctx context.Context, options agent.DiscoveryOptions, info proto.SupportedAgentKind) *agent.Runtime {
	return discoverWithCheck(ctx, options, info, CheckCLIAvailable)
}
func discoverWithCheck(parent context.Context, options agent.DiscoveryOptions, result proto.SupportedAgentKind, check func(context.Context, string) (string, error)) *agent.Runtime {
	runtime := &agent.Runtime{Info: result, Session: Factory, SessionCapabilityContext: true, ExecutorCapabilityContext: true}

	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	defer cancel()
	version, err := check(ctx, "")
	if err != nil {
		fmt.Fprintf(options.Stderr, "oac-daemon: mcode unavailable: %v\n  Install: npm install -g @minimax-ai/code@0.4.12\n", err)
		return runtime
	}
	result.Available, result.Version = true, version
	if SupportsExecution(version) {
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
	runtime.Info = result
	workspace := discoverWorkspace(parent, options, runtime)
	if runtime.Info.Available {
		runtime.Executor = NewExecutorFactory(workspace)
	}
	if workspace != nil {
		runtime.SessionCapabilityContext = false
		runtime.Info.Capabilities.WorkspaceAuthoring = proto.CapabilityUnsupported
		runtime.Preparation = NewPreparationFactory(*workspace)
		runtime.WorkspaceReadPreparation = true
	}
	fmt.Fprintf(options.Stdout, "mcode preflight ok (%s)\n", version)
	return runtime
}
