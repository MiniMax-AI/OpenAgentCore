package main

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"

	"github.com/MiniMax-AI/OpenAgentCore/internal/obs/log"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/execution"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
)

func executionConcurrency() (int, error) {
	value, explicit := os.LookupEnv("OAC_EXECUTION_CONCURRENCY")
	if !explicit {
		return execution.DefaultExecutionConcurrency, nil
	}
	limit, err := strconv.Atoi(value)
	if err != nil || limit < 1 || limit > 1024 {
		return 0, errors.New("OAC_EXECUTION_CONCURRENCY must be an integer between 1 and 1024")
	}
	return limit, nil
}

func validateProcessConfiguration() error {
	if path := os.Getenv("OAC_SETTINGS_FILE"); path != "" && !filepath.IsAbs(path) {
		return errors.New("OAC_SETTINGS_FILE must be an absolute path")
	}
	return nil
}

// publicURL reads OAC_PUBLIC_URL, the one origin applications, nodes,
// sandboxes and self-hosted executors use. An empty result disables daemon
// transport, as for a Core without execution.
func publicURL() (string, error) {
	value := os.Getenv("OAC_PUBLIC_URL")
	if value == "" {
		return "", nil
	}
	if store.ValidateSandboxCoreURL(value) != nil {
		return "", errors.New("OAC_PUBLIC_URL must be a canonical HTTPS origin without path, credentials, query or fragment, such as https://core.example; plain HTTP is accepted only for a loopback host")
	}
	return value, nil
}

// The launcher loads core.env. Core reports its sources without parsing another
// configuration layer or logging environment values.
func logConfigurationSources() {
	if path := os.Getenv("OAC_SETTINGS_FILE"); path != "" {
		log.Bg().Info("Core process configuration from the installer", "settings", path)
	} else {
		log.Bg().Info("Core process configuration loaded from the process environment")
	}
	for _, key := range []string{"OAC_HISTORY_SETTINGS_FILE"} {
		if path := os.Getenv(key); path != "" {
			log.Bg().Info("Core auxiliary configuration", "setting", key, "path", path)
		}
	}
}
