package cli

import (
	"context"
	"fmt"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/paths"
)

type discoveredHarness struct {
	declaration agent.Declaration
	runtime     agent.Runtime
}
type agentCLIDiscovery []discoveredHarness

func preflightAgentCLIs(parent context.Context, rc *runContext, profile string) (agentCLIDiscovery, error) {
	return discoverAgentCLIs(parent, rc, profile, harnessDeclarations)
}
func discoverAgentCLIs(parent context.Context, rc *runContext, profile string, declarations []agent.Declaration) (agentCLIDiscovery, error) {
	runtimeRoot, err := paths.Root()
	if err != nil {
		return nil, err
	}
	stateRoot, err := paths.StateDirectory()
	if err != nil {
		return nil, err
	}
	var out agentCLIDiscovery
	available := false
	for _, declaration := range declarations {
		if rc.installedKinds != nil && !rc.installedKinds[declaration.Kind] {
			continue
		}
		runtime := declaration.Discover(parent, agent.DiscoveryOptions{Profile: profile, StateRoot: stateRoot, RuntimeRoot: runtimeRoot, Stdout: rc.stdout, Stderr: rc.stderr}, declaration.Info())
		if runtime == nil {
			continue
		}
		out = append(out, discoveredHarness{declaration, *runtime})
		available = available || runtime.Info.Available
	}
	if err := parent.Err(); err != nil {
		return out, err
	}
	if !available {
		return out, fmt.Errorf("connect: no supported agent CLI available (install a supported Harness runtime)")
	}
	return out, nil
}
