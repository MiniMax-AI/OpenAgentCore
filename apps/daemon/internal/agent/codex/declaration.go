package codex

import (
	"context"
	"fmt"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	configuration "github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig/codex"
)

// Declaration owns Codex discovery, configuration and execution factories.
var Declaration = agent.Declaration{Info: proto.SupportedAgentKind{
	Kind: "codex",
	Capabilities: proto.AgentKindCapabilities{
		SubagentObservations:           proto.CapabilitySupported,
		Streaming:                      proto.CapabilitySupported,
		Permissions:                    proto.CapabilitySupported,
		Usage:                          proto.CapabilitySupported,
		Resume:                         proto.CapabilitySupported,
		NativeSessionRecovery:          proto.CapabilityUnsupported,
		WorkspaceAuthoring:             proto.CapabilitySupported,
		Steering:                       proto.CapabilitySupported,
		MessageItems:                   proto.CapabilitySupported,
		ToolObservations:               proto.CapabilitySupported,
		EnvironmentNone:                proto.CapabilitySupported,
		LocalEnvironment:               proto.CapabilityUnsupported,
		Preparation:                    proto.CapabilityUnsupported,
		WorkspaceReadPreparation:       proto.CapabilityUnsupported,
		WorkspaceOutputExport:          proto.CapabilityUnsupported,
		ProgrammaticToolCallingDisable: proto.CapabilitySupported,
		WebSearchControl:               proto.CapabilitySupported,
		ExecutionControls:              proto.CapabilityFromBool(SupportsTextVerbosity),
		TextVerbosity:                  proto.CapabilityFromBool(SupportsTextVerbosity),
		StructuredOutput:               proto.CapabilityUnsupported,
		ToolSearch:                     proto.CapabilityUnsupported,
		MessageImages:                  proto.CapabilitySupported,
		FunctionResultImages:           proto.CapabilitySupported,
		SubagentControl:                proto.CapabilitySupported,
		DurableInputReceipts:           proto.CapabilitySupported,
		DurableTurns:                   proto.CapabilitySupported,
		FunctionTools:                  proto.CapabilitySupported,
		MCPHTTPTools:                   proto.CapabilitySupported,
		MCPHTTPRequired:                proto.CapabilityUnsupported,
		MCPHTTPBearerAuth:              proto.CapabilitySupported,
	},
}, Configuration: configuration.Configuration(), Discover: discover}

func discover(ctx context.Context, options agent.DiscoveryOptions, info proto.SupportedAgentKind) *agent.Runtime {
	return discoverWithCheck(ctx, options, info, CheckCLIAvailable)
}
func discoverWithCheck(parent context.Context, options agent.DiscoveryOptions, info proto.SupportedAgentKind, check func(context.Context, string) (string, error)) *agent.Runtime {
	runtime := &agent.Runtime{Info: info, Session: Factory, SessionCapabilityContext: true, ExecutorCapabilityContext: true}
	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	defer cancel()
	version, err := check(ctx, "")
	if err != nil {
		fmt.Fprintf(options.Stderr, "oac-daemon: codex unavailable: %v\n  Install: %s\n", err, InstallURL)
		return runtime
	}
	runtime.Info.Available, runtime.Info.Version = true, version
	caps := &runtime.Info.Capabilities
	caps.NativeSessionRecovery = proto.CapabilityFromBool(SupportsNativeSessionRecovery(version))
	caps.LocalEnvironment = proto.CapabilityFromBool(SupportsLocalEnvironment(version))
	caps.MCPHTTPRequired = proto.CapabilityFromBool(SupportsNativeSessionRecovery(version))
	runtime.Executor = NewExecutorFactory()
	if caps.LocalEnvironment.IsSupported() {
		runtime.WorkspaceReadPreparation = true
		runtime.Preparation = func(ctx context.Context, req proto.PromptRequestPayload) (agent.Prepared, error) {
			prepared, err := Prepare(ctx, req)
			if prepared == nil {
				return nil, err
			}
			return prepared, err
		}
	}
	fmt.Fprintf(options.Stdout, "Codex preflight ok (%s)\n", version)
	return runtime
}
