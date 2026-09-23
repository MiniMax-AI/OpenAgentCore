package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/runtimehistory"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/runtimehistory/clickhousereader"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/runtimeobs"
)

type panicHistoryExporter struct{}

func (panicHistoryExporter) Export(context.Context, runtimeobs.ExportRecord) error { return nil }
func (panicHistoryExporter) Close(context.Context) error                           { panic("close") }

type fakeRuntimeHistoryReader struct {
	capabilities runtimehistory.Capabilities
	closed       bool
}

func (r *fakeRuntimeHistoryReader) Capabilities() runtimehistory.Capabilities { return r.capabilities }
func (r *fakeRuntimeHistoryReader) Query(context.Context, runtimehistory.Query) (runtimehistory.Result, error) {
	return runtimehistory.Result{}, nil
}
func (r *fakeRuntimeHistoryReader) Close() error { r.closed = true; return nil }

func TestRuntimeHistoryIsDisabledByDefault(t *testing.T) {
	t.Setenv("AGENTS_API_RUNTIME_HISTORY_FILE", "")
	setup, err := runtimeHistory(t.Context())
	if err != nil || setup.Option != nil || setup.Exporter != nil || setup.Reader != nil || setup.SampleInterval != 0 {
		t.Fatalf("disabled history created dependencies: option=%v exporter=%v reader=%v interval=%v err=%v", setup.Option != nil, setup.Exporter != nil, setup.Reader != nil, setup.SampleInterval, err)
	}
}

func TestRuntimeHistoryWiresStrictClickHouseReaderWithoutExposingBackend(t *testing.T) {
	file := filepath.Join(t.TempDir(), "runtime-history.json")
	config := `{"transport":"otlp_http","endpoint":"https://collector.example.test/v1/metrics","sample_interval_seconds":30,"clickhouse":{"address":"clickhouse.example.test:9440","database":"runtime_history","username":"reader","password":"must-not-leak","secure":true,"dial_timeout_seconds":4,"query_timeout_seconds":6}}`
	if err := os.WriteFile(file, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTS_API_RUNTIME_HISTORY_FILE", file)
	original := openRuntimeHistoryReader
	defer func() { openRuntimeHistoryReader = original }()
	var captured clickhousereader.Config
	reader := &fakeRuntimeHistoryReader{}
	openRuntimeHistoryReader = func(_ context.Context, value clickhousereader.Config) (runtimeHistoryReader, error) {
		captured = value
		reader.capabilities = value.Capabilities
		return reader, nil
	}
	setup, err := runtimeHistory(t.Context())
	if err != nil || setup.Reader != reader {
		t.Fatalf("ClickHouse Reader was not wired: reader=%v err=%v", setup.Reader != nil, err)
	}
	if captured.Address != "clickhouse.example.test:9440" || captured.Database != "runtime_history" || captured.Username != "reader" || !captured.Secure || captured.DialTimeout != 4*time.Second || captured.QueryTimeout != 6*time.Second || !captured.Capabilities.Durable() {
		t.Fatalf("unexpected ClickHouse Reader configuration: %+v", captured)
	}
	closeRuntimeHistoryReader(setup.Reader)
	if !reader.closed {
		t.Fatal("ClickHouse Reader was not closed")
	}
	closeCtx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	closeRuntimeHistory(closeCtx, setup.Exporter)
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
		{name: "implicit insecure ClickHouse", config: `{"transport":"otlp_http","endpoint":"https://collector.example.test/v1/metrics","clickhouse":{"address":"clickhouse.example.test:9000","database":"runtime_history","username":"reader","password":"must-not-leak"}}`},
		{name: "conflicting ClickHouse transport", config: `{"transport":"otlp_http","endpoint":"https://collector.example.test/v1/metrics","clickhouse":{"address":"clickhouse.example.test:9440","database":"runtime_history","username":"reader","secure":true,"insecure":true}}`},
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
		ClickHouse: &runtimeHistoryClickHouseConfig{
			Address: "127.0.0.1:9000", Database: "runtime_history", Username: "reader", Insecure: true,
		},
	}
	if err := validateRuntimeHistoryConfig(config); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeHistoryClosePanicIsIsolated(t *testing.T) {
	closeRuntimeHistory(t.Context(), panicHistoryExporter{})
}
