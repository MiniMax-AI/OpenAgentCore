package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/runtimeobs"
)

type panicHistoryExporter struct{}

func (panicHistoryExporter) Export(context.Context, runtimeobs.ExportRecord) error { return nil }
func (panicHistoryExporter) Close(context.Context) error                           { panic("close") }

func TestRuntimeHistoryIsDisabledByDefault(t *testing.T) {
	t.Setenv("AGENTS_API_RUNTIME_HISTORY_FILE", "")
	setup, err := runtimeHistory(t.Context())
	if err != nil || setup.Option != nil || setup.Exporter != nil || setup.SampleInterval != 0 {
		t.Fatalf("disabled history created dependencies: option=%v exporter=%v interval=%v err=%v", setup.Option != nil, setup.Exporter != nil, setup.SampleInterval, err)
	}
}

func TestRuntimeHistoryLoadsStrictServerOnlyConfig(t *testing.T) {
	file := filepath.Join(t.TempDir(), "runtime-history.json")
	config := `{"transport":"otlp_http","endpoint":"https://collector.example.test/v1/metrics","headers":{"Authorization":"Bearer test-only"},"queue_capacity":12,"timeout_seconds":3,"sample_interval_seconds":30}`
	if err := os.WriteFile(file, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTS_API_RUNTIME_HISTORY_FILE", file)
	setup, err := runtimeHistory(t.Context())
	if err != nil || setup.Option == nil || setup.Exporter == nil || setup.SampleInterval != 30*time.Second {
		t.Fatalf("valid history config was rejected: option=%v exporter=%v interval=%v err=%v", setup.Option != nil, setup.Exporter != nil, setup.SampleInterval, err)
	}
	closeCtx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := setup.Exporter.Close(closeCtx); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeHistoryConfigFailsClosedWithoutLeakingSecrets(t *testing.T) {
	tests := []struct {
		name   string
		config string
	}{
		{name: "unknown field", config: `{"transport":"otlp_http","endpoint":"https://collector.example.test/v1/metrics","secret":"must-not-leak"}`},
		{name: "implicit insecure", config: `{"transport":"otlp_http","endpoint":"http://collector.example.test/v1/metrics"}`},
		{name: "userinfo", config: `{"transport":"otlp_http","endpoint":"https://user:must-not-leak@collector.example.test/v1/metrics"}`},
		{name: "header newline", config: "{\"transport\":\"otlp_http\",\"endpoint\":\"https://collector.example.test/v1/metrics\",\"headers\":{\"Authorization\":\"Bearer must-not-leak\\n\"}}"},
		{name: "reserved header", config: `{"transport":"otlp_http","endpoint":"https://collector.example.test/v1/metrics","headers":{"Host":"must-not-leak"}}`},
		{name: "oversized queue", config: `{"transport":"otlp_http","endpoint":"https://collector.example.test/v1/metrics","queue_capacity":4097}`},
		{name: "oversized timeout", config: `{"transport":"otlp_http","endpoint":"https://collector.example.test/v1/metrics","timeout_seconds":31}`},
		{name: "too frequent sampling", config: `{"transport":"otlp_http","endpoint":"https://collector.example.test/v1/metrics","sample_interval_seconds":4}`},
		{name: "oversized sampling interval", config: `{"transport":"otlp_http","endpoint":"https://collector.example.test/v1/metrics","sample_interval_seconds":301}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "runtime-history.json")
			if err := os.WriteFile(file, []byte(test.config), 0600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("AGENTS_API_RUNTIME_HISTORY_FILE", file)
			setup, err := runtimeHistory(t.Context())
			if err == nil || setup.Option != nil || setup.Exporter != nil {
				t.Fatalf("unsafe history config was accepted: option=%v exporter=%v err=%v", setup.Option != nil, setup.Exporter != nil, err)
			}
			if strings.Contains(err.Error(), "must-not-leak") {
				t.Fatalf("history error leaked config content: %v", err)
			}
		})
	}
}

func TestRuntimeHistoryAllowsExplicitLocalHTTPCollector(t *testing.T) {
	config := runtimeHistoryConfig{
		Transport: "otlp_http", Endpoint: "http://127.0.0.1:4318/v1/metrics", Insecure: true,
		Headers: map[string]string{"X-Scope-OrgID": "operator-history"},
	}
	if err := validateRuntimeHistoryConfig(config); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeHistoryClosePanicIsIsolated(t *testing.T) {
	closeRuntimeHistory(t.Context(), panicHistoryExporter{})
}
