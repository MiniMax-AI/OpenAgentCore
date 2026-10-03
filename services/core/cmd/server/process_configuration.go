package main

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"

	"github.com/MiniMax-AI/OpenAgentCore/internal/obs/log"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/execution"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
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
	if deployment.ValidateCoreURL(value) != nil {
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

func providerProcessPaths() sandbox.ProcessPaths {
	return sandbox.ProcessPaths{ArtifactRoot: os.Getenv("OAC_PROVIDER_ROOT"), StateRoot: os.Getenv("OAC_PROVIDER_STATE_ROOT")}
}

// sandboxCapacity is read once during process construction. The installer derives
// these variables from core.sandbox_capacity in its configuration schema.
type sandboxCapacity struct {
	MaxActive   int
	MaxRetained int
}

func configuredSandboxCapacity() (sandboxCapacity, error) {
	capacity := sandboxCapacity{MaxActive: 100, MaxRetained: 400}
	for _, setting := range []struct {
		name   string
		target *int
	}{
		{"OAC_SANDBOX_MAX_ACTIVE", &capacity.MaxActive},
		{"OAC_SANDBOX_MAX_RETAINED", &capacity.MaxRetained},
	} {
		if raw, present := os.LookupEnv(setting.name); present {
			value, err := strconv.Atoi(raw)
			if err != nil || value < 1 || value > 100000 {
				return sandboxCapacity{}, errors.New(setting.name + " must be an integer between 1 and 100000")
			}
			*setting.target = value
		}
	}
	if capacity.MaxRetained < capacity.MaxActive {
		return sandboxCapacity{}, errors.New("OAC_SANDBOX_MAX_RETAINED must be at least OAC_SANDBOX_MAX_ACTIVE")
	}
	return capacity, nil
}
