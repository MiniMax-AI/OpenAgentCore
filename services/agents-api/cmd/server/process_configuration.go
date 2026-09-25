package main

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"

	"github.com/MiniMax-AI-Dev/parsar/internal/obs/log"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/execution"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

func executionConcurrency() (int, error) {
	value, explicit := os.LookupEnv("AGENTS_API_EXECUTION_CONCURRENCY")
	if !explicit {
		return execution.DefaultExecutionConcurrency, nil
	}
	limit, err := strconv.Atoi(value)
	if err != nil || limit < 1 || limit > 1024 {
		return 0, errors.New("AGENTS_API_EXECUTION_CONCURRENCY must be an integer between 1 and 1024")
	}
	return limit, nil
}

func validateProcessConfiguration() error {
	// A renamed setting fails instead of being read under its old name.
	if _, present := os.LookupEnv("AGENTS_API_SANDBOX_ADMIN_DIGESTS_FILE"); present {
		return errors.New("AGENTS_API_SANDBOX_ADMIN_DIGESTS_FILE was renamed; set AGENTS_API_CORE_KEY_DIGESTS_FILE to the Core key digest file instead")
	}
	if _, present := os.LookupEnv("AGENTS_API_DAEMON_WS_URL"); present {
		return errors.New("AGENTS_API_DAEMON_WS_URL is retired; set AGENTS_API_PUBLIC_URL to the public Core origin, such as https://core.example, and Core derives the daemon WebSocket URL")
	}
	if _, present := os.LookupEnv("AGENTS_API_CONFIG_FILE"); present {
		return errors.New("AGENTS_API_CONFIG_FILE is retired and has no replacement; remove it")
	}
	for _, retired := range []string{"AGENTS_API_MANAGED_RUNTIMES_FILE", "AGENTS_API_SANDBOX_NODE_STATE_DIR", "AGENTS_API_SANDBOX_NODE_CORE_URL"} {
		if _, present := os.LookupEnv(retired); present {
			return errors.New(retired + " is retired; configure deployment in Core and enroll a separate node")
		}
	}
	if path := os.Getenv("AGENTS_API_SETTINGS_FILE"); path != "" && !filepath.IsAbs(path) {
		return errors.New("AGENTS_API_SETTINGS_FILE must be an absolute path")
	}
	return nil
}

// publicURL reads AGENTS_API_PUBLIC_URL, the one origin applications, nodes,
// sandboxes and self-hosted executors use. An empty result disables daemon
// transport, as for a Core without execution.
func publicURL() (string, error) {
	value := os.Getenv("AGENTS_API_PUBLIC_URL")
	if value == "" {
		return "", nil
	}
	if store.ValidateSandboxCoreURL(value) != nil {
		return "", errors.New("AGENTS_API_PUBLIC_URL must be a canonical HTTPS origin without path, credentials, query or fragment, such as https://core.example; plain HTTP is accepted only for a loopback host")
	}
	return value, nil
}

// The launcher loads core.env. Core reports its sources without parsing another
// configuration layer or logging environment values.
func logConfigurationSources() {
	if path := os.Getenv("AGENTS_API_SETTINGS_FILE"); path != "" {
		log.Bg().Info("Core process configuration from the installer", "settings", path)
	} else {
		log.Bg().Info("Core process configuration loaded from the process environment")
	}
	for _, key := range []string{"AGENTS_API_EXECUTION_OPTIONS_FILE", "AGENTS_API_RUNTIME_HISTORY_FILE"} {
		if path := os.Getenv(key); path != "" {
			log.Bg().Info("Core auxiliary configuration", "setting", key, "path", path)
		}
	}
}
