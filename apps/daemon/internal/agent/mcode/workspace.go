package mcode

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/localworkspace"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

// WorkspaceConfig is frozen deployment input, separate from public Agent options.
type WorkspaceConfig struct {
	Binary, Node, Companion, Directory string
}

func ConfigureLocal(binary, node, companion, workspace string) (WorkspaceConfig, error) {
	c := WorkspaceConfig{Binary: binary, Node: node, Companion: companion, Directory: workspace}
	if runtime.GOOS == "windows" {
		return c, fmt.Errorf("mcode: native execution requires Linux or macOS")
	}
	for _, path := range []string{binary, node, companion, workspace} {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path || path == "/" {
			return c, fmt.Errorf("mcode: canonical absolute deployment paths are required")
		}
	}
	for _, path := range []string{binary, node} {
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			return c, fmt.Errorf("mcode: runtime program unavailable")
		}
	}
	bound, err := os.Stat(workspace)
	if err != nil || !bound.IsDir() {
		return c, fmt.Errorf("mcode: workspace directory unavailable")
	}
	return c, nil
}

func prepareWorkspaceOptions(runtimeRoot string, c WorkspaceConfig, req proto.PromptRequestPayload) (launchOptions, error) {
	if req.LocalEnvironment == nil || req.LocalEnvironment.WorkspaceRoot != c.Directory || req.DisableExecutionEnvironment || req.WorkspaceReadOnly {
		return launchOptions{}, fmt.Errorf("mcode: execution does not match the dedicated workspace")
	}
	values, err := localworkspace.ReadOptionalToolEnvironment()
	if err != nil {
		return launchOptions{}, err
	}
	// A disabled web_search control removes web_fetch as well; without the control the native default applies.
	searchDisabled := req.ExecutionControls != nil && req.ExecutionControls.WebSearch != nil && req.ExecutionControls.WebSearch.Mode == "disabled"
	profile := &workspaceProfile{WebFetch: !searchDisabled}
	for _, key := range slices.Sorted(maps.Keys(values)) {
		profile.ToolEnv = append(profile.ToolEnv, key+"="+values[key])
	}
	// Reuse public option validation and private Session state provisioning.
	// The native process runs in the declared workspace.
	private := req
	private.LocalEnvironment, private.DisableExecutionEnvironment = nil, true
	opts, err := prepareOptions(runtimeRoot, private, profile)
	if err != nil {
		return opts, err
	}
	opts.Dir = c.Directory
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
