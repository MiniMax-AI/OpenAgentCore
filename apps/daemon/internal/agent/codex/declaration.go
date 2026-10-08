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
// Discovery narrows the declared support to what the installed version and
// platform serve.
var Declaration = agent.Declaration{Info: proto.SupportedAgentKind{Kind: "codex", Capabilities: configuration.Configuration().Declaration.Capabilities},
	Configuration: configuration.Configuration(), Discover: discover}

func discover(ctx context.Context, options agent.DiscoveryOptions, info proto.SupportedAgentKind) *agent.Runtime {
	return discoverWithCheck(ctx, options, info, CheckCLIAvailable)
}
func discoverWithCheck(parent context.Context, options agent.DiscoveryOptions, info proto.SupportedAgentKind, check func(context.Context, string) (string, error)) *agent.Runtime {
	runtime := &agent.Runtime{Info: info}
	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	defer cancel()
	version, err := check(ctx, "")
	if err != nil {
		fmt.Fprintf(options.Stderr, "oac-daemon: codex unavailable: %v\n  Install: %s\n", err, InstallURL)
		return runtime
	}
	runtime.Info.Available, runtime.Info.Version = true, version
	caps := &runtime.Info.Capabilities
	// A local Environment and required MCP servers need native Session recovery.
	recovery := proto.CapabilityFromBool(SupportsNativeSessionRecovery(version))
	caps.NativeSessionRecovery, caps.LocalEnvironment, caps.MCPHTTPRequired = recovery, recovery, recovery
	caps.TextVerbosity = proto.CapabilityFromBool(SupportsTextVerbosity)
	runtime.View = discoverView(version)
	fmt.Fprintf(options.Stdout, "Codex preflight ok (%s)\n", version)
	return runtime
}
