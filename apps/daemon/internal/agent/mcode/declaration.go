package mcode

import (
	"context"
	"fmt"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	configuration "github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig/mcode"
)

// Declaration owns MiniMax Code discovery, configuration and agent-host view.
// Native preparation verifies the applied admission and tool profile before
// input.
var Declaration = agent.Declaration{Info: proto.SupportedAgentKind{Kind: "mcode", Capabilities: configuration.Configuration().Declaration.Capabilities},
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
		fmt.Fprintf(options.Stderr, "oac-daemon: mcode unavailable: %v\n  Install: npm install -g @minimax-ai/code@%s\n", err, SupportedVersion)
		return runtime
	}
	runtime.Info.Available, runtime.Info.Version = true, version
	runtime.View = discoverView(options)
	fmt.Fprintf(options.Stdout, "mcode preflight ok (%s)\n", version)
	return runtime
}
