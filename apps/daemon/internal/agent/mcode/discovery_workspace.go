package mcode

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent/binpath"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/localworkspace"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

func discoverWorkspace(parent context.Context, options agent.DiscoveryOptions, runtime *agent.Runtime) *WorkspaceConfig {
	fail := func(err error) {
		*runtime = *agent.Unavailable(runtime.Info)
		fmt.Fprintf(options.Stderr, "oac-daemon: mcode workspace unavailable: %v\n", err)
	}
	binding, err := localworkspace.Load()
	if err != nil {
		fail(err)
		return nil
	}
	if binding == nil {
		narrowWorkspace(&runtime.Info.Capabilities, false)
		return nil
	}
	node := os.Getenv("OAC_RUNTIME_MCODE_NODE")
	if node == "" {
		node = "node"
	}
	node, err = exec.LookPath(node)
	if err != nil {
		fail(err)
		return nil
	}
	node, err = filepath.Abs(node)
	if err != nil {
		fail(err)
		return nil
	}
	binary, err := exec.LookPath(binpath.MCode())
	if err != nil {
		fail(err)
		return nil
	}
	binary, err = filepath.Abs(binary)
	if err != nil {
		fail(err)
		return nil
	}
	c, err := ConfigureLocal(binary, node, os.Getenv("OAC_RUNTIME_MCODE_COMPANION"), os.Getenv("OAC_RUNTIME_WORKSPACE"))
	if err == nil {
		err = CheckWorkspace(parent, c)
	}
	if err != nil {
		fail(err)
		return nil
	}

	narrowWorkspace(&runtime.Info.Capabilities, true)
	return &c
}

// narrowWorkspace keeps either the local workspace capabilities or environment:none.
func narrowWorkspace(caps *proto.AgentKindCapabilities, local bool) {
	agent.Narrow(&caps.EnvironmentNone, !local)
	agent.Narrow(&caps.LocalEnvironment, local)
	agent.Narrow(&caps.WorkspaceReadPreparation, local)
	agent.Narrow(&caps.WorkspaceOutputExport, local)
}
