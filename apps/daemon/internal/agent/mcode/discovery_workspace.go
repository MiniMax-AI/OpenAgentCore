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
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/paths"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

func discoverWorkspace(parent context.Context, options agent.DiscoveryOptions, runtime *agent.Runtime) *WorkspaceConfig {
	fail := func(err error) {
		runtime.Info.Available = false
		fmt.Fprintf(options.Stderr, "oac-daemon: mcode workspace unavailable: %v\n", err)
	}
	binding, err := localworkspace.Load()
	if err != nil {
		fail(err)
		return nil
	}
	if binding == nil {
		return nil
	}
	if !runtime.Info.Available || !SupportsExecution(runtime.Info.Version) {
		fail(fmt.Errorf("local execution requires the qualified native version"))
		return nil
	}
	root, err := paths.Root()
	if err != nil {
		fail(err)
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
	c, err := ConfigureLocal(binary, node, os.Getenv("OAC_RUNTIME_MCODE_WORKSPACE_BRIDGE"), root, os.Getenv("OAC_RUNTIME_WORKSPACE"), binding.NetworkPolicy())
	if err == nil {
		err = CheckWorkspace(parent, c)
	}
	if err != nil {
		fail(err)
		return nil
	}

	caps := &runtime.Info.Capabilities
	caps.EnvironmentNone = proto.CapabilityUnsupported
	caps.Preparation, caps.LocalEnvironment = proto.CapabilitySupported, proto.CapabilitySupported
	caps.WorkspaceReadPreparation = proto.CapabilitySupported
	return &c
}
