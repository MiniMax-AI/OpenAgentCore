package main

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"

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
	var problems, renamed []string
	for _, setting := range [][2]string{
		{"AGENTS_API_ADDR", "OAC_ADDR"},
		{"AGENTS_API_PUBLIC_URL", "OAC_PUBLIC_URL"},
		{"AGENTS_API_DATABASE_URL", "OAC_DATABASE_URL"},
		{"AGENTS_API_DATABASE_PASSWORD_FILE", "OAC_DATABASE_PASSWORD_FILE"},
		{"AGENTS_API_CREDENTIAL_KEY_FILE", "OAC_CREDENTIAL_KEY_FILE"},
		{"AGENTS_API_CORE_KEY_DIGESTS_FILE", "OAC_CORE_KEY_DIGESTS_FILE"},
		{"AGENTS_API_SANDBOX_INSTALLATION_ID", "OAC_INSTALLATION_ID"},
		{"AGENTS_API_SETTINGS_FILE", "OAC_SETTINGS_FILE"},
		{"AGENTS_API_E2B_STATE_DIR", "OAC_E2B_STATE_DIR"},
		{"AGENTS_API_E2B_PROVIDER_BIN", "OAC_E2B_PROVIDER_BIN"},
		{"AGENTS_API_ENGINE", "OAC_DEFAULT_HARNESS"},
		{"AGENTS_API_HARNESSES", "OAC_HARNESSES"},
		{"AGENTS_API_EXECUTION_CONCURRENCY", "OAC_EXECUTION_CONCURRENCY"},
		{"AGENTS_API_WRITE_AUDIT_RETENTION", "OAC_WRITE_AUDIT_RETENTION"},
		{"AGENTS_API_OAUTH_TRUSTED_ORIGINS", "OAC_OAUTH_TRUSTED_ORIGINS"},
		{"AGENTS_API_RUNTIME_HISTORY_FILE", "OAC_HISTORY_SETTINGS_FILE"},
	} {
		if _, present := os.LookupEnv(setting[0]); present {
			renamed = append(renamed, setting[0]+" → "+setting[1])
		}
	}
	renamed = append(renamed, log.RenamedEnvironment()...)
	if len(renamed) > 0 {
		problems = append(problems, "OpenAgentCore renamed these settings; set the new names and remove the old ones: "+strings.Join(renamed, ", "))
	}
	for _, setting := range [][2]string{
		{"AGENTS_API_SANDBOX_ADMIN_DIGESTS_FILE", "was renamed; set OAC_CORE_KEY_DIGESTS_FILE to the Core key digest file instead"},
		{"AGENTS_API_DAEMON_WS_URL", "is retired; set OAC_PUBLIC_URL to the public Core origin, such as https://core.example, and Core derives the daemon WebSocket URL"},
		{"AGENTS_API_CONFIG_FILE", "is retired and has no replacement; remove it"},
		{"AGENTS_API_MANAGED_RUNTIMES_FILE", "is retired; configure deployment in Core and enroll a separate node"},
		{"AGENTS_API_SANDBOX_NODE_STATE_DIR", "is retired; configure deployment in Core and enroll a separate node"},
		{"AGENTS_API_SANDBOX_NODE_CORE_URL", "is retired; configure deployment in Core and enroll a separate node"},
		{"AGENTS_API_EXECUTION_OPTIONS_FILE", "is retired; remove it and set deployment default model providers in Web (System) or with PUT /core/v1/harnesses/{harness}/model-configuration"},
	} {
		if _, present := os.LookupEnv(setting[0]); present {
			problems = append(problems, setting[0]+" "+setting[1])
		}
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
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
