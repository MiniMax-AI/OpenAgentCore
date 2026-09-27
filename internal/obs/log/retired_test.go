package log

import (
	"log/slog"
	"strings"
	"testing"
)

func TestRenamedLoggingSettings(t *testing.T) {
	for _, suffix := range []string{"LEVEL", "FORMAT", "ADD_SOURCE"} {
		t.Setenv("PARSAR_LOG_"+suffix, "")
	}
	t.Setenv("OAC_LOG_LEVEL", "debug")
	t.Setenv("OAC_LOG_FORMAT", "text")
	t.Setenv("OAC_LOG_ADD_SOURCE", "1")
	renamed := RenamedEnvironment()
	if len(renamed) != 3 {
		t.Fatalf("expected all three retired names, got %v", renamed)
	}
	for _, suffix := range []string{"LEVEL", "FORMAT", "ADD_SOURCE"} {
		if !strings.Contains(strings.Join(renamed, ","), "PARSAR_LOG_"+suffix+" → OAC_LOG_"+suffix) {
			t.Fatalf("missing replacement: %v", renamed)
		}
	}
	cfg := ConfigFromEnv()
	if cfg.Level != slog.LevelDebug || cfg.Format != "text" || !cfg.AddSource {
		t.Fatalf("new logging settings not read: %+v", cfg)
	}
}
