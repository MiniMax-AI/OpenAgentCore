package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigRejectsUnsafeURLsAndSecretFiles(t *testing.T) {
	directory := t.TempDir()
	token, password := filepath.Join(directory, "caller.key"), filepath.Join(directory, "console.password")
	for name, value := range map[string]string{token: "private-core-token\n", password: "private-console-password\n"} {
		if err := os.WriteFile(name, []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("CORE_CONSOLE_TOKEN_FILE", token)
	t.Setenv("CORE_CONSOLE_PASSWORD_FILE", password)
	t.Setenv("CORE_CONSOLE_ORIGIN", testOrigin)
	t.Setenv("CORE_CONSOLE_UPSTREAM", "http://core:8091")
	t.Setenv("CORE_CONSOLE_DIST", directory)
	c, err := loadConfig()
	if err != nil || c.token != "private-core-token" {
		t.Fatalf("valid configuration failed: %v", err)
	}
	for _, value := range []string{"http://user:secret@core:8091", "http://core:8091/v1", "http://core:8091?token=secret", "http://core:8091#", "file:///config/caller.key", ""} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("CORE_CONSOLE_UPSTREAM", value)
			_, err := loadConfig()
			if err == nil || strings.Contains(err.Error(), "secret") {
				t.Fatal("unsafe URL accepted or exposed")
			}
		})
	}
	for _, value := range []string{"", "token with spaces", strings.Repeat("x", 4097), "token\x00"} {
		if err := os.WriteFile(token, []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := loadConfig(); err == nil {
			t.Fatal("invalid token accepted")
		}
	}
	if err := os.WriteFile(token, []byte("valid-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(token, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadConfig(); err == nil {
		t.Fatal("publicly readable secret accepted")
	}
}
