package main

import (
	"errors"
	"os"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/execution"
	sandboxconfig "github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox/config"
)

type managedRuntimeConfig = sandboxconfig.Config
type managedDockerConfig = sandboxconfig.Docker

func loadManagedRuntime() (sandboxconfig.Config, *sandboxconfig.Built, func(), error) {
	closeProvider := func() {}
	file := os.Getenv("AGENTS_API_MANAGED_RUNTIMES_FILE")
	if file == "" {
		return sandboxconfig.Config{}, nil, closeProvider, nil
	}
	if os.Getenv("AGENTS_API_DAEMON_WS_URL") == "" {
		return sandboxconfig.Config{}, nil, closeProvider, errors.New("managed Runtime configuration requires AGENTS_API_DAEMON_WS_URL")
	}
	config, err := sandboxconfig.Load(file)
	if err != nil {
		return config, nil, closeProvider, err
	}
	built, closeProvider, err := sandboxconfig.Build(config)
	return config, built, closeProvider, err
}

func runtimeFromConfig(config sandboxconfig.Config, built *sandboxconfig.Built) *execution.RuntimeProvider {
	result := &execution.RuntimeProvider{CoreURL: config.CoreURL, ProviderKind: config.Provider, InstallationID: config.InstallationID, BackendFingerprint: built.BackendFingerprint, Provider: built.Provider, Maintenance: config.Maintenance}
	if p := built.Suspension; p != nil {
		result.Suspension = &execution.RuntimeSuspensionPolicy{IdleTimeout: p.IdleTimeout, Retention: p.Retention, MaxActive: p.MaxActive, MaxRetained: p.MaxRetained}
	}
	return result
}

func managedRuntimes() (*execution.RuntimeProvider, func(), error) {
	config, built, close, err := loadManagedRuntime()
	if err != nil || built == nil {
		return nil, close, err
	}
	return runtimeFromConfig(config, built), close, nil
}
func managedBackendFingerprint(kind, namespace string) string {
	return sandboxconfig.BackendFingerprint(kind, namespace)
}

func managedRuntimeProviderKind(runtime *execution.RuntimeProvider) string {
	if runtime == nil {
		return ""
	}
	return runtime.ProviderKind
}
