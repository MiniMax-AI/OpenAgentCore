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
