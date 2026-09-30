package main

import (
	"os"
	"strings"
	"testing"
)

func TestExecutionConcurrencyConfiguration(t *testing.T) {
	t.Setenv("OAC_EXECUTION_CONCURRENCY", "unused")
	if err := os.Unsetenv("OAC_EXECUTION_CONCURRENCY"); err != nil {
		t.Fatal(err)
	}
	if got, err := executionConcurrency(); err != nil || got != 4 {
		t.Fatal(got, err)
	}
	for _, value := range []string{"1", "7", "1024"} {
		t.Setenv("OAC_EXECUTION_CONCURRENCY", value)
		if got, err := executionConcurrency(); err != nil || got < 1 {
			t.Fatal(value, got, err)
		}
	}
	for _, value := range []string{"", "0", "-1", "1025", "1.5", "secret-value"} {
		t.Setenv("OAC_EXECUTION_CONCURRENCY", value)
		if _, err := executionConcurrency(); err == nil || strings.Contains(err.Error(), "secret-value") {
			t.Fatal("invalid concurrency accepted or echoed", err)
		}
	}
}

func TestRetiredConfigurationRejectedWithoutReadingValues(t *testing.T) {
	for _, key := range []string{"AGENTS_API_MANAGED_RUNTIMES_FILE", "AGENTS_API_SANDBOX_NODE_STATE_DIR", "AGENTS_API_SANDBOX_NODE_CORE_URL"} {
		t.Run(key, func(t *testing.T) {
			t.Setenv(key, "private-value")
			err := validateProcessConfiguration()
			if err == nil || !strings.Contains(err.Error(), key) || strings.Contains(err.Error(), "private-value") {
				t.Fatal(err)
			}
		})
	}
}

func TestRetiredAddressAndConfigSettingsNameTheirReplacement(t *testing.T) {
	for key, replacement := range map[string]string{"AGENTS_API_DAEMON_WS_URL": "OAC_PUBLIC_URL", "AGENTS_API_CONFIG_FILE": "remove it"} {
		t.Run(key, func(t *testing.T) {
			t.Setenv(key, "wss://private.example/api/v1/agent-daemon/ws")
			err := validateProcessConfiguration()
			if err == nil || !strings.Contains(err.Error(), replacement) || strings.Contains(err.Error(), "private.example") {
				t.Fatal(err)
			}
		})
	}
}

func TestPublicURLMustBeACanonicalOrigin(t *testing.T) {
	for _, value := range []string{"https://core.example", "https://core.example:8443", "http://127.0.0.1:8091"} {
		t.Setenv("OAC_PUBLIC_URL", value)
		if got, err := publicURL(); err != nil || got != value {
			t.Fatal(value, got, err)
		}
	}
	for _, value := range []string{"https://core.example/", "https://Core.example", "http://core.example", "wss://core.example", "https://core.example/v1"} {
		t.Setenv("OAC_PUBLIC_URL", value)
		if _, err := publicURL(); err == nil {
			t.Fatal("accepted", value)
		}
	}
}

func TestRenamedCoreKeyDigestSettingNamesItsReplacement(t *testing.T) {
	t.Setenv("AGENTS_API_SANDBOX_ADMIN_DIGESTS_FILE", "/private/admin/digests.json")
	err := validateProcessConfiguration()
	if err == nil || !strings.Contains(err.Error(), "OAC_CORE_KEY_DIGESTS_FILE") || strings.Contains(err.Error(), "/private/") {
		t.Fatal(err)
	}
}

func TestRetiredExecutionOptionsFileNamesItsReplacement(t *testing.T) {
	for _, value := range []string{"/private/execution-options.json", ""} {
		t.Setenv("AGENTS_API_EXECUTION_OPTIONS_FILE", value)
		err := validateProcessConfiguration()
		if err == nil || !strings.Contains(err.Error(), "remove it") || !strings.Contains(err.Error(), "/core/v1/harnesses/{harness}/model-configuration") || strings.Contains(err.Error(), "/private/") {
			t.Fatal(err)
		}
	}
}

func TestRenamedProcessSettingsFailClosed(t *testing.T) {
	for _, suffix := range []string{"ADDR", "PUBLIC_URL", "DATABASE_URL", "DATABASE_PASSWORD_FILE", "CREDENTIAL_KEY_FILE", "CORE_KEY_DIGESTS_FILE", "SANDBOX_INSTALLATION_ID", "SETTINGS_FILE", "E2B_STATE_DIR", "E2B_PROVIDER_BIN", "ENGINE", "HARNESSES", "EXECUTION_CONCURRENCY", "WRITE_AUDIT_RETENTION", "OAUTH_TRUSTED_ORIGINS", "RUNTIME_HISTORY_FILE"} {
		t.Run(suffix, func(t *testing.T) {
			old := "AGENTS_API_" + suffix
			t.Setenv(old, "")
			if err := validateProcessConfiguration(); err == nil || !strings.Contains(err.Error(), old+" → OAC_") {
				t.Fatalf("empty renamed setting accepted: %v", err)
			}
		})
	}
	t.Setenv("AGENTS_API_PUBLIC_URL", "private-url")
	t.Setenv("OAC_PUBLIC_URL", "https://core.example")
	t.Setenv("AGENTS_API_ENGINE", "private-engine")
	t.Setenv("AGENTS_API_CONFIG_FILE", "private-file")
	t.Setenv("PARSAR_LOG_LEVEL", "private-log")
	err := validateProcessConfiguration()
	if err == nil {
		t.Fatal("old and new names were accepted together")
	}
	for _, want := range []string{"AGENTS_API_PUBLIC_URL → OAC_PUBLIC_URL", "AGENTS_API_ENGINE → OAC_DEFAULT_HARNESS", "AGENTS_API_CONFIG_FILE is retired", "PARSAR_LOG_LEVEL → OAC_LOG_LEVEL"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("missing %s in %v", want, err)
		}
	}
	if strings.Contains(err.Error(), "private-") {
		t.Fatalf("setting value exposed: %v", err)
	}
}
