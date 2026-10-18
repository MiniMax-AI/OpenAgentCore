package codex

import (
	"context"
	"fmt"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	declaration "github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig/codex"
)

// Declaration binds the shared Codex declaration to discovery and execution factories.
var Declaration = agent.Declaration{Declaration: declaration.Declaration, Discover: discover}

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
		return agent.Unavailable(info)
	}
	runtime.Info.Available, runtime.Info.Version = true, version
	caps := &runtime.Info.Capabilities
	recovery, local := SupportsNativeSessionRecovery(version), SupportsLocalEnvironment(version)
	agent.Narrow(&caps.NativeSessionRecovery, recovery)
	agent.Narrow(&caps.MCPHTTPRequired, recovery)
	for _, capability := range []*proto.CapabilitySupport{&caps.LocalEnvironment, &caps.WorkspaceReadPreparation, &caps.WorkspaceOutputExport, &caps.RetainedNativeHistory} {
		agent.Narrow(capability, local)
	}
	runtime.Executor = NewExecutorFactory(options.StateRoot)
	fmt.Fprintf(options.Stdout, "Codex preflight ok (%s)\n", version)
	return runtime
}
