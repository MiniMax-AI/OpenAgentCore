package mcode

import (
	"context"
	"encoding/json"
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
	// Reuse public option validation and private Session state provisioning.
	// The native process, ACP Session and workspace tools share the declared cwd.
	private := req
	private.LocalEnvironment, private.DisableExecutionEnvironment = nil, true
	// Public declarations have already been resolved into the transient ACP map.
	private.MCPHTTPServers = nil
	opts, err := prepareOptionsWithSkills(ctx, private, false)
	if err != nil {
		return opts, err
	}
	opts.Dir, opts.bindings = c.Directory, bindings
	var skills []string
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
			skills = append(skills, skill.Metadata.Name)
		}
	}
	profile := map[string]any{"capabilityRoot": req.LocalEnvironment.CapabilityRoot, "workspace": c.Directory, "scratch": c.Scratch, "network": c.Network, "allowedDomains": (agentnetwork.Policy{Access: c.Network, AllowedDomains: c.AllowedDomains}).Hosts(), "skills": len(skills) > 0}
	file, err := localworkspace.ToolEnvironmentFile()
	if err != nil {
		return opts, err
	}
	if file != "" {
		profile["toolEnvFile"] = file
	}
	data, err := os.OpenRoot(opts.DataDir)
	if err != nil {
		return opts, err
	}
	defer data.Close()
	return opts, writeWorkspaceTools(&opts, data, req, workspaceTools{node: c.Node, bridge: c.Bridge, profile: filepath.Join(opts.DataDir, "workspace-profile.json")}, profile, skills, servers)
}

// workspaceTools are the workspace bridge as the native process runs it, and
// the bridge's profile path.
type workspaceTools struct{ node, bridge, profile string }

// writeWorkspaceTools turns the native configuration in data over to the
// workspace bridge: native permissions and sandbox are off, the bridge's
// profile is written, and oac_workspace precedes the Session's servers.
func writeWorkspaceTools(opts *launchOptions, data *os.Root, req proto.PromptRequestPayload, tools workspaceTools, profile map[string]any, skills []string, servers []map[string]any) error {
	raw, err := data.ReadFile("config.yaml")
	if err != nil {
		return err
	}
	var config map[string]any
	if err = json.Unmarshal(raw, &config); err != nil {
		return err
	}
	config["permissionMode"] = "bypassPermissions"
	config["sandbox"] = map[string]bool{"enabled": false}
	if len(skills) > 0 {
		selected := config["agents"].(map[string]any)["default"].(map[string]any)
		selected["skills"] = skills
		for _, key := range []string{"tools", "builtinTools"} {
			selected[key] = append(selected[key].([]any), "skill")
		}
	}
	if raw, err = json.Marshal(config); err != nil {
		return err
	}
	if err = data.WriteFile("config.yaml", raw, 0600); err != nil {
		return err
	}
	if raw, err = json.Marshal(profile); err != nil {
		return err
	}
	if err = data.WriteFile("workspace-profile.json", raw, 0600); err != nil {
		return err
	}
	opts.MCP = []map[string]any{{"name": "oac_workspace", "command": tools.node, "args": []string{tools.bridge, tools.profile}, "env": []map[string]string{}}}
	opts.MCP = append(opts.MCP, servers...)
	if !req.DisableSubagents {
		// This native data directory belongs to one public Session and its
		// descendants. ACP's ephemeral server map otherwise covers only root.
		configured := map[string]any{}
		for _, server := range opts.MCP[:1] {
			name, _ := server["name"].(string)
			configured[name] = map[string]any{"type": "stdio", "command": server["command"], "args": server["args"], "env": map[string]string{}, "enabled": true}
		}
		raw, err := json.Marshal(map[string]any{"mcpServers": configured})
		if err != nil {
			return err
		}
		if err := data.WriteFile("mcp.json", raw, 0o600); err != nil {
			return err
		}
	}
	return nil
}
