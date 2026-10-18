// Package pi implements the Runtime adapter for the Pi coding agent. Pi runs as
// a child process and speaks its native RPC mode over stdio; the adapter never
// reimplements Pi's model or tool loop.
package pi

import (
	"context"
	"fmt"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	configuration "github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig/pi"
)

// Declaration owns Pi discovery, configuration and execution factories.
var Declaration = agent.Declaration{Info: proto.SupportedAgentKind{
	Kind: "pi",
	Capabilities: proto.AgentKindCapabilities{
		SubagentObservations:           proto.CapabilityUnsupported,
		Streaming:                      proto.CapabilitySupported,
		Usage:                          proto.CapabilitySupported,
		Resume:                         proto.CapabilitySupported,
		RetainedNativeHistory:          proto.CapabilitySupported,
		NativeSessionRecovery:          proto.CapabilityUnsupported,
		Steering:                       proto.CapabilitySupported,
		MessageItems:                   proto.CapabilitySupported,
		ToolObservations:               proto.CapabilitySupported,
		EnvironmentNone:                proto.CapabilityUnsupported,
		LocalEnvironment:               proto.CapabilitySupported,
		Preparation:                    proto.CapabilityUnsupported,
		WorkspaceReadPreparation:       proto.CapabilityUnsupported,
		WorkspaceOutputExport:          proto.CapabilityUnsupported,
		ProgrammaticToolCallingDisable: proto.CapabilityUnsupported,
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
		MCPHTTPTools:                   proto.CapabilityUnsupported,
		MCPHTTPRequired:                proto.CapabilityUnsupported,
		MCPHTTPBearerAuth:              proto.CapabilityUnsupported,
	},
}, Configuration: configuration.Configuration(), Discover: discover}

func discover(parent context.Context, options agent.DiscoveryOptions, info proto.SupportedAgentKind) *agent.Runtime {
	runtime := &agent.Runtime{Info: info}
	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	defer cancel()
	version, err := CheckCLIAvailable(ctx, "")
	if err != nil {
		fmt.Fprintf(options.Stderr, "oac-daemon: pi unavailable: %v\n", err)
		return runtime
	}
	runtime.Info.Available, runtime.Info.Version = true, version
	runtime.Info.Capabilities.WorkspaceReadPreparation = proto.CapabilityFromBool(
		runtime.Info.Capabilities.LocalEnvironment.IsSupported())
	runtime.Executor = NewExecutorFactory(options.StateRoot, options.RuntimeRoot)
	fmt.Fprintf(options.Stdout, "Pi preflight ok (%s)\n", version)
	return runtime
}
