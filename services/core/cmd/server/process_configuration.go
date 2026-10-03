package main

import (
	"os"

	"github.com/MiniMax-AI/OpenAgentCore/internal/obs/log"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

// The launcher loads core.env. Core reports its sources without parsing another
// configuration layer or logging environment values.
func logConfigurationSources() {
	log.Bg().Info("Core process configuration loaded from the process environment")
	for _, key := range []string{"OAC_HISTORY_SETTINGS_FILE"} {
		if path := os.Getenv(key); path != "" {
			log.Bg().Info("Core auxiliary configuration", "setting", key, "path", path)
		}
	}
}

func providerProcessPaths() sandbox.ProcessPaths {
	return sandbox.ProcessPaths{ArtifactRoot: os.Getenv("OAC_PROVIDER_ROOT"), StateRoot: os.Getenv("OAC_PROVIDER_STATE_ROOT")}
}
