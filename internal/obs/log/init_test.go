package log

import (
	"log/slog"
	"strings"
	"testing"
)

func TestLoadConfigIsStrictAndEmptyMeansDefault(t *testing.T) {
	t.Setenv("OAC_LOG_LEVEL", "")
	t.Setenv("OAC_LOG_FORMAT", "auto")
	t.Setenv("OAC_LOG_ADD_SOURCE", "")
	if cfg, err := LoadConfig(); err != nil || cfg.Level != slog.LevelInfo || cfg.Format != "" || cfg.AddSource {
		t.Fatal(cfg, err)
	}
	t.Setenv("OAC_LOG_LEVEL", "warn")
	t.Setenv("OAC_LOG_FORMAT", "text")
	t.Setenv("OAC_LOG_ADD_SOURCE", "1")
	if cfg, err := LoadConfig(); err != nil || cfg.Level != slog.LevelWarn || cfg.Format != "text" || !cfg.AddSource {
		t.Fatal(cfg, err)
	}
	for name, value := range map[string]string{"OAC_LOG_LEVEL": "warning", "OAC_LOG_FORMAT": "JSON", "OAC_LOG_ADD_SOURCE": "true"} {
		t.Run(name, func(t *testing.T) {
			t.Setenv(name, value)
			if _, err := LoadConfig(); err == nil || !strings.Contains(err.Error(), name) || strings.Contains(err.Error(), value) {
				t.Fatal(err)
			}
		})
	}
}
