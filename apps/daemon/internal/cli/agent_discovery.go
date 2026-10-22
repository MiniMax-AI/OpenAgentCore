package cli

import (
	"bytes"
	"context"
	"fmt"
	"sync"

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
	// Preflights are independent and sit on every cold start, so they run
	// concurrently; their output is replayed in declaration order.
	type probe struct {
		runtime        *agent.Runtime
		stdout, stderr bytes.Buffer
	}
	probes := make([]probe, len(declarations))
	var running sync.WaitGroup
	for i, declaration := range declarations {
		if rc.installedKinds != nil && !rc.installedKinds[declaration.Kind] {
			continue
		}
		running.Go(func() {
			p := &probes[i]
			p.runtime = declaration.Discover(parent, agent.DiscoveryOptions{Profile: profile, StateRoot: stateRoot, RuntimeRoot: runtimeRoot, Stdout: &p.stdout, Stderr: &p.stderr}, declaration.Info())
		})
	}
	running.Wait()
	var out agentCLIDiscovery
	available := false
	for i := range probes {
		p := &probes[i]
		_, _ = rc.stdout.Write(p.stdout.Bytes())
		_, _ = rc.stderr.Write(p.stderr.Bytes())
		if p.runtime == nil {
			continue
		}
		out = append(out, discoveredHarness{declarations[i], *p.runtime})
		available = available || p.runtime.Info.Available
	}
	if err := parent.Err(); err != nil {
		return out, err
	}
	if !available {
		return out, fmt.Errorf("connect: no supported agent CLI available (install a supported Harness runtime)")
	}
	return out, nil
}
