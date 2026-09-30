package main

import (
	"context"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimeobs"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type panicHistoryExporter struct{}

func (panicHistoryExporter) Export(context.Context, runtimeobs.ExportRecord) error { return nil }
func (panicHistoryExporter) Close(context.Context) error                           { panic("close") }

func TestRuntimeHistoryUsesCoreDatabaseByDefault(t *testing.T) {
	t.Setenv("OAC_HISTORY_SETTINGS_FILE", "")
	for _, enabled := range []bool{true, false} {
		setup, err := runtimeHistory(t.Context(), store.New(nil), enabled)
		if err != nil {
			t.Fatal(err)
		}
		if len(setup.Options) != 1 || setup.Exporter != nil || setup.Reader == nil || setup.Prune == nil {
			t.Fatal("default history requires extra deployment")
		}
		capabilities := setup.Reader.Capabilities()
		if capabilities.Durable() != enabled || capabilities.Retention != 7*24*time.Hour {
			t.Fatalf("incorrect default capabilities: %+v", capabilities)
		}
		if enabled && setup.SampleInterval != 30*time.Second {
			t.Fatal("default cadence missing")
		}
		if !enabled && setup.SampleInterval != 0 {
			t.Fatal("sampler requires an execution owner")
		}
	}
}

func TestRuntimeHistoryOptionalExportAndSamplingConfiguration(t *testing.T) {
	file := filepath.Join(t.TempDir(), "history.json")
	if err := os.WriteFile(file, []byte(`{"transport":"otlp_http","endpoint":"https://collector.example.test/v1/metrics","headers":{"Authorization":"Bearer private"},"sample_interval_seconds":60}`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OAC_HISTORY_SETTINGS_FILE", file)
	setup, err := runtimeHistory(t.Context(), store.New(nil), true)
	if err != nil {
		t.Fatal(err)
	}
	defer closeRuntimeHistory(t.Context(), setup.Exporter)
	if len(setup.Options) != 2 || setup.SampleInterval != time.Minute || setup.Reader.Capabilities().MinimumStep != time.Minute {
		t.Fatal("export and local history are not independent")
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
		{name: "removed backend", config: `{"clickhouse":{"password":"must-not-leak"}}`},
		{name: "transport without endpoint", config: `{"transport":"otlp_http"}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "runtime-history.json")
			if err := os.WriteFile(file, []byte(test.config), 0600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("OAC_HISTORY_SETTINGS_FILE", file)
			_, err := loadRuntimeHistoryConfig()
			if err == nil {
				t.Fatal("unsafe history configuration accepted")
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
