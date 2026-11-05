package claudesdk

import (
	"fmt"
	"os"
	"strings"
)

// ConfigureLocal binds the dedicated workspace and keeps only the workspace
// environment names of the process environment.
func ConfigureLocal(config Config, workspace string) (Config, error) {
	if !canonicalWorkspaceDir(workspace) {
		return Config{}, fmt.Errorf("claudesdk: invalid trusted workspace configuration")
	}
	config.Workspace = workspace
	config.Env = nil
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if workspaceEnvName(name) {
			config.Env = append(config.Env, entry)
		}
	}
	return config, nil
}
