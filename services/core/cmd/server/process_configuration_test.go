package main

import (
	"os"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/processconfig"
)

func TestExecutionConcurrencyConfiguration(t *testing.T) {
	t.Setenv("OAC_EXECUTION_CONCURRENCY", "unused")
	if err := os.Unsetenv("OAC_EXECUTION_CONCURRENCY"); err != nil {
		t.Fatal(err)
	}
	if got, err := processconfig.ExecutionConcurrency(); err != nil || got != 4 {
		t.Fatal(got, err)
	}
	t.Setenv("OAC_EXECUTION_CONCURRENCY", "")
	if got, err := processconfig.ExecutionConcurrency(); err != nil || got != 4 {
		t.Fatal("empty concurrency did not keep the default", got, err)
	}
	for _, value := range []string{"1", "7", "1024"} {
		t.Setenv("OAC_EXECUTION_CONCURRENCY", value)
		if got, err := processconfig.ExecutionConcurrency(); err != nil || got < 1 {
			t.Fatal(value, got, err)
		}
	}
	for _, value := range []string{"0", "-1", "1025", "1.5", "secret-value"} {
		t.Setenv("OAC_EXECUTION_CONCURRENCY", value)
		if _, err := processconfig.ExecutionConcurrency(); err == nil || strings.Contains(err.Error(), "secret-value") {
			t.Fatal("invalid concurrency accepted or echoed", err)
		}
	}
}

func TestPublicURLMustBeACanonicalOrigin(t *testing.T) {
	for _, value := range []string{"https://core.example", "https://core.example:8443", "http://127.0.0.1:8091", "http://core.example"} {
		t.Setenv("OAC_PUBLIC_URL", value)
		if got, err := processconfig.PublicURL(); err != nil || got != value {
			t.Fatal(value, got, err)
		}
	}
	for _, value := range []string{"https://core.example/", "https://Core.example", "wss://core.example", "https://core.example/v1"} {
		t.Setenv("OAC_PUBLIC_URL", value)
		if _, err := processconfig.PublicURL(); err == nil {
			t.Fatal("accepted", value)
		}
	}
}
