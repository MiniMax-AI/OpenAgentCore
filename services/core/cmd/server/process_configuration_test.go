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

func TestPublicURLInsecureOptOut(t *testing.T) {
	t.Setenv("OAC_PUBLIC_URL_INSECURE", "0")
	t.Setenv("OAC_PUBLIC_URL", "http://core.internal:8091")
	if _, err := publicURL(); err == nil {
		t.Fatal("accepted plain HTTP outside loopback without the opt-in")
	}
	t.Setenv("OAC_PUBLIC_URL_INSECURE", "1")
	for _, value := range []string{"http://core.internal:8091", "http://10.0.0.5:8091", "http://localhost:8091"} {
		t.Setenv("OAC_PUBLIC_URL", value)
		if got, err := publicURL(); err != nil || got != value {
			t.Fatal(value, got, err)
		}
	}
	for _, value := range []string{"http://core.internal:8091/", "http://core.internal:8091/v1", "ws://core.internal:8091", "http://user:secret@core.internal"} {
		t.Setenv("OAC_PUBLIC_URL", value)
		if _, err := publicURL(); err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatal("accepted or echoed", value, err)
		}
	}
	t.Setenv("OAC_PUBLIC_URL", "https://core.example")
	if _, err := publicURL(); err == nil {
		t.Fatal("accepted the opt-in for an HTTPS public URL")
	}
	t.Setenv("OAC_PUBLIC_URL", "")
	if _, err := publicURL(); err == nil {
		t.Fatal("accepted the opt-in without a public URL")
	}
	t.Setenv("OAC_PUBLIC_URL", "http://core.internal:8091")
	for _, value := range []string{"true", "yes", "2", "unused-secret"} {
		t.Setenv("OAC_PUBLIC_URL_INSECURE", value)
		if _, err := publicURL(); err == nil || strings.Contains(err.Error(), value) {
			t.Fatal("accepted or echoed", value, err)
		}
	}
}
