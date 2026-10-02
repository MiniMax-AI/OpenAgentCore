package main

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"

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

// publicURLInsecure reads OAC_PUBLIC_URL_INSECURE. Omitted or empty selects the
// default; only "0" and "1" are accepted.
func publicURLInsecure() (bool, error) {
	value := os.Getenv("OAC_PUBLIC_URL_INSECURE")
	if value == "" {
		return false, nil
	}
	if value != "0" && value != "1" {
		return false, errors.New("OAC_PUBLIC_URL_INSECURE must be 0 or 1")
	}
	return value == "1", nil
}

// publicURL reads OAC_PUBLIC_URL, the one origin applications, nodes,
// sandboxes and self-hosted executors use. An empty result disables daemon
// transport, as for a Core without execution. OAC_PUBLIC_URL_INSECURE=1 accepts
// plain HTTP for a host that is not loopback, for a deployment that stays on a
// trusted network and has no certificate authority. The flag is meaningless
// without an origin and refused for an HTTPS one, so a forgotten or stale value
// stops startup instead of standing in for a policy nothing applies.
func publicURL() (string, error) {
	insecure, err := publicURLInsecure()
	if err != nil {
		return "", err
	}
	value := os.Getenv("OAC_PUBLIC_URL")
	if value == "" {
		if insecure {
			return "", errors.New("OAC_PUBLIC_URL_INSECURE requires OAC_PUBLIC_URL")
		}
		return "", nil
	}
	if insecure {
		if deployment.ValidateCoreURLInsecure(value) != nil {
			return "", errors.New("OAC_PUBLIC_URL must be a canonical HTTP or HTTPS origin without path, credentials, query or fragment, such as http://core.internal:8091, while OAC_PUBLIC_URL_INSECURE is 1")
		}
		if strings.HasPrefix(value, "https://") {
			return "", errors.New("OAC_PUBLIC_URL_INSECURE requires an http OAC_PUBLIC_URL")
		}
		return value, nil
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
