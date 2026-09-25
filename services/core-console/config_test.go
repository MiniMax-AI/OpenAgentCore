package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigRejectsUnsafeURLsAndSecretFiles(t *testing.T) {
	directory := t.TempDir()
	key := filepath.Join(directory, "core.key")
	valid := strings.Repeat("k", 32)
	if err := os.WriteFile(key, []byte(valid+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CORE_CONSOLE_CORE_KEY_FILE", key)
	t.Setenv("CORE_CONSOLE_ORIGIN", testOrigin)
	t.Setenv("CORE_CONSOLE_UPSTREAM", "http://core:8091")
	t.Setenv("CORE_CONSOLE_DIST", directory)
	c, err := loadConfig()
	if err != nil || c.coreKey != valid {
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
	for _, value := range []string{"", "token with spaces", strings.Repeat("x", 4097), "token\x00", strings.Repeat("s", 31)} {
		if err := os.WriteFile(key, []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := loadConfig(); err == nil || strings.Contains(err.Error(), "sss") {
			t.Fatal("invalid Core key accepted or echoed")
		}
	}
	if err := os.WriteFile(key, []byte(valid), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(key, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadConfig(); err == nil {
		t.Fatal("publicly readable secret accepted")
	}
}

func TestConfigRejectsRenamedAndRetiredSettings(t *testing.T) {
	directory := t.TempDir()
	key := filepath.Join(directory, "core.key")
	if err := os.WriteFile(key, []byte(strings.Repeat("k", 32)), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CORE_CONSOLE_CORE_KEY_FILE", key)
	t.Setenv("CORE_CONSOLE_DIST", directory)
	for setting, want := range map[string]string{
		"CORE_CONSOLE_ADMIN_TOKEN_FILE": "CORE_CONSOLE_CORE_KEY_FILE",
		"CORE_CONSOLE_AUTH_MODE":        "Core key",
		"CORE_CONSOLE_STATE_DIR":        "Core key",
		"CORE_CONSOLE_PASSWORD_FILE":    "Core key",
	} {
		t.Run(setting, func(t *testing.T) {
			t.Setenv(setting, key)
			_, err := loadConfig()
			if err == nil || !strings.Contains(err.Error(), setting) || !strings.Contains(err.Error(), want) || strings.Contains(err.Error(), key) {
				t.Fatalf("old setting error = %v", err)
			}
		})
	}
}
