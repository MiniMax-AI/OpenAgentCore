package claudesdk

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentnetwork"
)

// ConfigureLocal selects the qualified, dedicated Runtime layout. The shared
// localworkspace binding still authorizes every request against its Session.
func ConfigureLocal(config Config, root, workspace string, network agentnetwork.Policy) (Config, error) {
	config.StateDir = filepath.Join(root, "runtime", "claude-sdk", "history")
	config.Workspace = &WorkspaceConfig{
		Directory: workspace, PublicDirectory: workspace, NetworkAccess: network.Access, AllowedDomains: network.Hosts(),
		HomeDir:    filepath.Join(root, "runtime", "claude-sdk", "home"),
		ScratchDir: filepath.Join(root, "runtime", "claude-sdk", "scratch"),
	}
	if network.Validate() != nil {
		return Config{}, fmt.Errorf("claudesdk: dedicated Runtime requires an explicit network policy")
	}
	for _, dir := range []string{config.StateDir, config.Workspace.HomeDir, config.Workspace.ScratchDir} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return Config{}, err
		}
	}
	config.Env = nil
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if workspaceEnvName(name) {
			config.Env = append(config.Env, entry)
		}
	}
	_, _, err := workspaceEnvironment(config)
	return config, err
}
