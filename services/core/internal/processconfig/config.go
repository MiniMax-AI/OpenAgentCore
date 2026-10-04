// Package processconfig is the process settings Core loads from its environment.
// Startup and `oac-core check-config` both call Check. Settings reports the
// effective values for GET /core/v1/installation. Errors name the variable and
// never include its value.
package processconfig

import (
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/api"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/engine"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/execution"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/oauthrefresh"
)

// Check validates every process setting Core loads. An unset or empty
// variable keeps its default, so Compose can pass every setting through.
func Check() error {
	if _, err := PublicURL(); err != nil {
		return err
	}
	if _, err := ExecutionConcurrency(); err != nil {
		return err
	}
	if _, err := SandboxCapacity(); err != nil {
		return err
	}
	if _, err := InstallationID(); err != nil {
		return err
	}
	engineName, err := DefaultHarness()
	if err != nil {
		return err
	}
	if _, err := Harnesses(engineName); err != nil {
		return err
	}
	if _, err := writeAuditRetention(); err != nil {
		return err
	}
	if err := oauthOrigins(); err != nil {
		return err
	}
	return logSettings()
}

// Settings is the effective process configuration. Sensitive file settings
// report only whether they are configured.
func Settings() ([]api.InstallationSetting, error) {
	if err := Check(); err != nil {
		return nil, err
	}
	public, _ := PublicURL()
	concurrency, _ := ExecutionConcurrency()
	capacity, _ := SandboxCapacity()
	engineName, _ := DefaultHarness()
	enabled, _ := Harnesses(engineName)
	retention := "2160h"
	if value := os.Getenv("OAC_WRITE_AUDIT_RETENTION"); value != "" {
		retention = value
	}
	level, format, addSource := logValues()
	history := os.Getenv("OAC_HISTORY_SETTINGS_FILE") != ""
	origins := []string{}
	if raw := os.Getenv("OAC_OAUTH_TRUSTED_ORIGINS"); raw != "" {
		for _, origin := range strings.Split(raw, ",") {
			if origin = strings.TrimSpace(origin); origin != "" {
				origins = append(origins, origin)
			}
		}
	}
	var publicValue any
	if public != "" {
		publicValue = public
	}
	return []api.InstallationSetting{
		setting("public_url", publicValue, nil, true, []string{"core", "web"}),
		setting("log.level", level, "info", true, []string{"core", "web"}),
		setting("log.format", format, "auto", true, []string{"core", "web"}),
		setting("log.add_source", addSource, false, true, []string{"core", "web"}),
		setting("core.execution_concurrency", concurrency, execution.DefaultExecutionConcurrency, true, []string{"core"}),
		setting("core.sandbox_capacity.max_active", capacity.MaxActive, defaultSandboxMaxActive, true, []string{"core"}),
		setting("core.sandbox_capacity.max_retained", capacity.MaxRetained, defaultSandboxMaxRetained, true, []string{"core"}),
		setting("core.harnesses", enabled, (engine.Catalog{}).Kinds(), true, []string{"core"}),
		setting("core.default_harness", engineName, "codex", true, []string{"core"}),
		setting("core.write_audit_retention", retention, "2160h", true, []string{"core"}),
		setting("core.oauth_trusted_origins", origins, []string{}, true, []string{"core"}),
		sensitive("core.runtime_history", history, []string{"core"}),
	}, nil
}

func setting(key string, value, fallback any, changeable bool, restarts []string) api.InstallationSetting {
	return api.InstallationSetting{Key: key, Value: value, Default: fallback, Changeable: changeable, Sensitive: false, Restarts: restarts}
}

func sensitive(key string, configured bool, restarts []string) api.InstallationSetting {
	return api.InstallationSetting{Key: key, Configured: &configured, Changeable: true, Sensitive: true, Restarts: restarts}
}

// PublicURL reads OAC_PUBLIC_URL, the one origin applications, nodes,
// sandboxes and self-hosted executors use. An empty result disables daemon
// transport, as for a Core without execution.
func PublicURL() (string, error) {
	value := os.Getenv("OAC_PUBLIC_URL")
	if value == "" {
		return "", nil
	}
	if deployment.ValidateCoreURL(value) != nil {
		return "", configErr("OAC_PUBLIC_URL must be a canonical http or https origin without path, credentials, query or fragment, such as https://core.example")
	}
	return value, nil
}

// InstallationID reads the file named by OAC_INSTALLATION_ID_FILE. The ID
// enables the sandbox deployment and node routes; unset leaves them off.
func InstallationID() (string, error) {
	path := os.Getenv("OAC_INSTALLATION_ID_FILE")
	if path == "" {
		return "", nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", configErr("OAC_INSTALLATION_ID_FILE must name a readable file")
	}
	value := strings.TrimSpace(string(raw))
	if id, err := uuid.Parse(value); err != nil || id == uuid.Nil || id.String() != value {
		return "", configErr("OAC_INSTALLATION_ID_FILE must contain a canonical UUID")
	}
	return value, nil
}

// ExecutionConcurrency reads OAC_EXECUTION_CONCURRENCY. Unset or empty keeps
// the default.
func ExecutionConcurrency() (int, error) {
	value := os.Getenv("OAC_EXECUTION_CONCURRENCY")
	if value == "" {
		return execution.DefaultExecutionConcurrency, nil
	}
	limit, err := strconv.Atoi(value)
	if err != nil || limit < 1 || limit > 1024 {
		return 0, configErr("OAC_EXECUTION_CONCURRENCY must be an integer between 1 and 1024")
	}
	return limit, nil
}

// DefaultHarness reads OAC_DEFAULT_HARNESS, the Harness used when a request
// names none.
func DefaultHarness() (string, error) {
	value := os.Getenv("OAC_DEFAULT_HARNESS")
	if value == "" {
		return "codex", nil
	}
	if _, known := (engine.Catalog{}).Lookup(value); !known {
		return "", configErr("OAC_DEFAULT_HARNESS is not a known harness")
	}
	return value, nil
}

// Harnesses reads OAC_HARNESSES. Unset enables every Harness this build
// supports; a list supplements the default Harness.
func Harnesses(defaultEngine string) ([]string, error) {
	kinds := []string{defaultEngine}
	if value := os.Getenv("OAC_HARNESSES"); value != "" {
		kinds = append(kinds, strings.Split(value, ",")...)
	} else {
		kinds = append(kinds, (engine.Catalog{}).Kinds()...)
	}
	for i, kind := range kinds {
		kind = strings.TrimSpace(kind)
		if _, known := (engine.Catalog{}).Lookup(kind); !known {
			return nil, configErr("OAC_HARNESSES contains an unknown harness")
		}
		kinds[i] = kind
	}
	slices.Sort(kinds)
	return slices.Compact(kinds), nil
}

func writeAuditRetention() (time.Duration, error) {
	value := os.Getenv("OAC_WRITE_AUDIT_RETENTION")
	if value == "" {
		return 90 * 24 * time.Hour, nil
	}
	duration, parseErr := time.ParseDuration(value)
	if parseErr != nil || duration < time.Hour {
		return 0, configErr("OAC_WRITE_AUDIT_RETENTION must be a duration of at least 1h")
	}
	return duration, nil
}

func oauthOrigins() error {
	var origins []string
	if raw := os.Getenv("OAC_OAUTH_TRUSTED_ORIGINS"); raw != "" {
		for _, origin := range strings.Split(raw, ",") {
			origins = append(origins, strings.TrimSpace(origin))
		}
	}
	if _, err := oauthrefresh.NewClient(origins); err != nil {
		return configErr("OAC_OAUTH_TRUSTED_ORIGINS is invalid")
	}
	return nil
}

func logSettings() error {
	if value, ok := os.LookupEnv("OAC_LOG_LEVEL"); ok && value != "" && !slices.Contains([]string{"debug", "info", "warn", "warning", "error", "err"}, strings.ToLower(strings.TrimSpace(value))) {
		return configErr("OAC_LOG_LEVEL must be debug, info, warn or error")
	}
	if value, ok := os.LookupEnv("OAC_LOG_FORMAT"); ok && value != "" && !slices.Contains([]string{"auto", "json", "text"}, strings.ToLower(strings.TrimSpace(value))) {
		return configErr("OAC_LOG_FORMAT must be auto, json or text")
	}
	if value, ok := os.LookupEnv("OAC_LOG_ADD_SOURCE"); ok && value != "" && value != "0" && value != "1" {
		return configErr("OAC_LOG_ADD_SOURCE must be 0 or 1")
	}
	return nil
}

func logValues() (string, string, bool) {
	level := "info"
	if value := strings.ToLower(strings.TrimSpace(os.Getenv("OAC_LOG_LEVEL"))); value != "" {
		level = value
	}
	format := "auto"
	if value := strings.ToLower(strings.TrimSpace(os.Getenv("OAC_LOG_FORMAT"))); value != "" {
		format = value
	}
	return level, format, os.Getenv("OAC_LOG_ADD_SOURCE") == "1"
}

type configError string

func (e configError) Error() string { return string(e) }

func configErr(message string) error { return configError(message) }
