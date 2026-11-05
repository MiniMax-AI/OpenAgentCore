package claudesdk

import (
	"fmt"
	"os"
	"strings"
)

// ConfigureLocal selects the qualified, dedicated Runtime layout. The shared
// localworkspace binding still authorizes every request against its Session.
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
