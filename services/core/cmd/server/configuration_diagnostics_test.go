package main

import (
	"bytes"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/obs/log"
)

func TestConfigurationDiagnosticsPreserveFactsWithoutSecrets(t *testing.T) {
	tests := []struct {
		setting, content, reason string
		read                     func() error
	}{
		{"OAC_CREDENTIAL_KEY_FILE", "private-secret", "base64-encoded", func() error { _, err := credentialCipher(); return err }},
		{"OAC_CORE_KEY_DIGESTS_FILE", `["private-secret"]`, "unique lowercase SHA-256", func() error { _, err := deploymentAdminAuthenticator(); return err }},
		{"OAC_HISTORY_SETTINGS_FILE", `{"endpoint":"https://user:private-secret@example.test/metrics","transport":"otlp_http"}`, "canonical absolute", func() error { _, err := loadRuntimeHistoryConfig(); return err }},
	}
	for _, test := range tests {
		t.Run(test.setting, func(t *testing.T) {
			var output bytes.Buffer
			previous := slog.Default()
			slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
			t.Cleanup(func() { slog.SetDefault(previous) })
			path := filepath.Join(t.TempDir(), "settings")
			t.Setenv(test.setting, path)
			if err := test.read(); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("file cause was discarded: %v", err)
			}
			logged := output.String()
			if !strings.Contains(logged, test.setting) || !strings.Contains(logged, path) || !strings.Contains(logged, `"error_kind":"not_found"`) {
				t.Fatalf("missing actionable file diagnostic: %s", logged)
			}
			output.Reset()
			if err := os.WriteFile(path, []byte(test.content), 0600); err != nil {
				t.Fatal(err)
			}
			if err := test.read(); err == nil {
				t.Fatal("invalid file accepted")
			}
			logged = output.String()
			if !strings.Contains(logged, test.setting) || !strings.Contains(logged, test.reason) || strings.Contains(logged, "private-secret") {
				t.Fatalf("unsafe or incomplete validation diagnostic: %s", logged)
			}
		})
	}
}

func TestCoreRejectsInvalidDatabaseConfigurationBeforeMigrations(t *testing.T) {
	log.Init(log.ConfigFromEnv())
	for _, setting := range []string{"OAC_PUBLIC_URL", "OAC_INSTALLATION_ID_FILE", "OAC_EXECUTION_CONCURRENCY", "OAC_DEFAULT_HARNESS", "OAC_HARNESSES", "OAC_WRITE_AUDIT_RETENTION", "OAC_OAUTH_TRUSTED_ORIGINS", "OAC_LOG_LEVEL", "OAC_LOG_FORMAT", "OAC_LOG_ADD_SOURCE", "OAC_DATABASE_PASSWORD_FILE"} {
		t.Setenv(setting, "")
	}
	for _, databaseURL := range []string{"", "postgres://user:private-secret@%zz/db"} {
		t.Run(databaseURL[:min(len(databaseURL), 8)], func(t *testing.T) {
			var output bytes.Buffer
			previous := slog.Default()
			slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
			t.Cleanup(func() { slog.SetDefault(previous) })
			t.Setenv("OAC_DATABASE_URL", databaseURL)
			if err := run(); err == nil {
				t.Fatal("invalid database configuration accepted")
			}
			logged := output.String()
			if !strings.Contains(logged, `"setting":"OAC_DATABASE_URL"`) || !strings.Contains(logged, `"msg":"Stage failed"`) || !strings.Contains(logged, `"stage":"configuration"`) || strings.Contains(logged, "database_migrations") || strings.Contains(logged, "private-secret") {
				t.Fatalf("unsafe or misleading configuration diagnostic: %s", logged)
			}
		})
	}
}
