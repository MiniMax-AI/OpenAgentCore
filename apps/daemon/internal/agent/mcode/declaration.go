package mcode

import (
	"context"
	"fmt"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	declaration "github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig/mcode"
)

// Declaration binds the shared MiniMax Code declaration to discovery and execution factories.
var Declaration = agent.Declaration{Declaration: declaration.Declaration, Discover: discover}

func discover(ctx context.Context, options agent.DiscoveryOptions, info proto.SupportedAgentKind) *agent.Runtime {
	return discoverWithCheck(ctx, options, info, CheckCLIAvailable)
}
func discoverWithCheck(parent context.Context, options agent.DiscoveryOptions, result proto.SupportedAgentKind, check func(context.Context, string) (string, error)) *agent.Runtime {
	runtime := &agent.Runtime{Info: result}

	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	defer cancel()
	version, err := check(ctx, "")
	if err != nil {
		fmt.Fprintf(options.Stderr, "oac-daemon: mcode unavailable: %v\n  Install: npm install -g @minimax-ai/code@0.4.12\n", err)
		return agent.Unavailable(result)
	}
	result.Available, result.Version = true, version
	runtime.Info = result
	workspace := discoverWorkspace(parent, options, runtime)
	if runtime.Info.Available {
		runtime.Executor = NewExecutorFactory(options.RuntimeRoot, workspace)
	}
	fmt.Fprintf(options.Stdout, "mcode preflight ok (%s)\n", version)
	return runtime
}
