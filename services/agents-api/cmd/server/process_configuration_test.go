package main

import (
	"os"
	"strings"
	"testing"
)

func TestExecutionConcurrencyConfiguration(t *testing.T) {
	t.Setenv("AGENTS_API_EXECUTION_CONCURRENCY", "unused")
	if err := os.Unsetenv("AGENTS_API_EXECUTION_CONCURRENCY"); err != nil {
		t.Fatal(err)
	}
	if got, err := executionConcurrency(); err != nil || got != 4 {
		t.Fatal(got, err)
	}
	for _, value := range []string{"1", "7", "1024"} {
		t.Setenv("AGENTS_API_EXECUTION_CONCURRENCY", value)
		if got, err := executionConcurrency(); err != nil || got < 1 {
			t.Fatal(value, got, err)
		}
	}
	for _, value := range []string{"", "0", "-1", "1025", "1.5", "secret-value"} {
		t.Setenv("AGENTS_API_EXECUTION_CONCURRENCY", value)
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
	for key, replacement := range map[string]string{"AGENTS_API_DAEMON_WS_URL": "AGENTS_API_PUBLIC_URL", "AGENTS_API_CONFIG_FILE": "remove it"} {
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
		t.Setenv("AGENTS_API_PUBLIC_URL", value)
		if got, err := publicURL(); err != nil || got != value {
			t.Fatal(value, got, err)
		}
	}
	for _, value := range []string{"https://core.example/", "https://Core.example", "http://core.example", "wss://core.example", "https://core.example/v1"} {
		t.Setenv("AGENTS_API_PUBLIC_URL", value)
		if _, err := publicURL(); err == nil {
			t.Fatal("accepted", value)
		}
	}
}

func TestRenamedCoreKeyDigestSettingNamesItsReplacement(t *testing.T) {
	t.Setenv("AGENTS_API_SANDBOX_ADMIN_DIGESTS_FILE", "/private/admin/digests.json")
	err := validateProcessConfiguration()
	if err == nil || !strings.Contains(err.Error(), "AGENTS_API_CORE_KEY_DIGESTS_FILE") || strings.Contains(err.Error(), "/private/") {
		t.Fatal(err)
	}
}

func TestRetiredExecutionOptionsFileNamesItsReplacement(t *testing.T) {
	for _, value := range []string{"/private/execution-options.json", ""} {
		t.Setenv("AGENTS_API_EXECUTION_OPTIONS_FILE", value)
		err := validateProcessConfiguration()
		if err == nil || !strings.Contains(err.Error(), "remove it") || !strings.Contains(err.Error(), "/core/v1/harnesses/{harness}/model-provider") || strings.Contains(err.Error(), "/private/") {
			t.Fatal(err)
		}
	}
}
