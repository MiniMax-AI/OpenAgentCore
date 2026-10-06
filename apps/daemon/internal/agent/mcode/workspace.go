package mcode

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/localworkspace"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentnetwork"
)

// WorkspaceConfig is frozen deployment input, separate from public Agent options.
type WorkspaceConfig struct {
	Binary, Node, Bridge, Directory, Network, Scratch string
	AllowedDomains                                    []string
}

func ConfigureLocal(binary, node, bridge, root, workspace string, network agentnetwork.Policy) (WorkspaceConfig, error) {
	c := WorkspaceConfig{Binary: binary, Node: node, Bridge: bridge, Directory: workspace, Network: network.Access, AllowedDomains: network.Hosts(),
		Scratch: filepath.Join(root, "runtime", "mcode-tools", "scratch")}
	if runtime.GOOS == "windows" || network.Access != "enabled" || len(network.AllowedDomains) != 0 {
		return c, fmt.Errorf("mcode: native execution requires Linux or macOS and unrestricted host access")
	}
	if network.Validate() != nil {
		return c, fmt.Errorf("mcode: explicit workspace network policy is required")
	}
	for _, path := range []string{binary, node, bridge, root, workspace} {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path || path == "/" {
			return c, fmt.Errorf("mcode: canonical absolute deployment paths are required")
		}
	}
	for _, path := range []string{binary, node, bridge} {
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			return c, fmt.Errorf("mcode: runtime program unavailable")
		}
	}
	bound, err := os.Stat(workspace)
	if err != nil || !bound.IsDir() {
		return c, fmt.Errorf("mcode: workspace directory unavailable")
	}

	if err := os.MkdirAll(c.Scratch, 0700); err != nil {
		return c, err
	}
	return c, nil
}

func prepareWorkspaceOptions(ctx context.Context, c WorkspaceConfig, req proto.PromptRequestPayload) (launchOptions, error) {
	if c.Network != "enabled" || len(c.AllowedDomains) != 0 {
		return launchOptions{}, fmt.Errorf("mcode: Runtime does not implement network isolation")
	}
	if !req.StrictResume || req.LocalEnvironment == nil || req.LocalEnvironment.WorkspaceRoot != c.Directory || req.DisableExecutionEnvironment || !(agentnetwork.Policy{Access: c.Network, AllowedDomains: c.AllowedDomains}).Equal(agentnetwork.Policy{Access: req.LocalEnvironment.NetworkAccess, AllowedDomains: req.LocalEnvironment.AllowedDomains}) || req.WorkspaceReadOnly {
		return launchOptions{}, fmt.Errorf("mcode: execution does not match the dedicated workspace")
	}
	servers, bindings, err := runtimeMCP(req)
	if err != nil {
		return launchOptions{}, err
	}
	tools := workspaceTools{node: c.Node, bridge: c.Bridge, profile: map[string]any{"capabilityRoot": req.LocalEnvironment.CapabilityRoot, "workspace": c.Directory, "scratch": c.Scratch, "network": c.Network, "allowedDomains": (agentnetwork.Policy{Access: c.Network, AllowedDomains: c.AllowedDomains}).Hosts(), "skills": len(req.LocalEnvironment.Skills) > 0}}
	file, err := localworkspace.ToolEnvironmentFile()
	if err != nil {
		return launchOptions{}, err
	}
	if file != "" {
		tools.profile["toolEnvFile"] = file
	}
	for _, skill := range req.LocalEnvironment.Skills {
		tools.skills = append(tools.skills, skill.Metadata.Name)
	}
	// Reuse public option validation and private Session state provisioning.
	// The native process, ACP Session and workspace tools share the declared cwd.
	private := req
	private.LocalEnvironment, private.DisableExecutionEnvironment = nil, true
	// Public declarations have already been resolved into the transient ACP map.
	private.MCPHTTPServers = nil
	opts, err := prepareOptionsWithTools(ctx, private, &tools)
	if err != nil {
		return opts, err
	}
	opts.Dir, opts.bindings = c.Directory, bindings
	opts.MCP = append([]map[string]any{tools.server(opts.DataDir)}, servers...)
	if len(req.LocalEnvironment.Skills) > 0 {
		root := filepath.Join(opts.DataDir, "skills")
		if err := os.MkdirAll(root, 0700); err != nil {
			return opts, err
		}
		for _, skill := range req.LocalEnvironment.Skills {
			link, target := filepath.Join(root, skill.Metadata.Name), localworkspace.SkillPath(skill)
			actual, err := os.Readlink(link)
			if err == nil {
				if actual != target {
					return opts, fmt.Errorf("mcode: unexpected native Skill root")
				}
			} else if !os.IsNotExist(err) {
				return opts, err
			} else if err := os.Symlink(target, link); err != nil {
				return opts, err
			}
		}
	}
	return opts, nil
}

// workspaceTools is the workspace bridge as the native process runs it, the
// bridge's profile and the Skills it presents.
type workspaceTools struct {
	node, bridge string
	profile      map[string]any
	skills       []string
}

// server is the bridge's ACP MCP server, with its profile in dataDir as the
// native process sees it.
func (t workspaceTools) server(dataDir string) map[string]any {
	return map[string]any{"name": "oac_workspace", "command": t.node, "args": []string{t.bridge, filepath.Join(dataDir, "workspace-profile.json")}, "env": []map[string]string{}}
}
