package main

import (
	"context"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/processconfig"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimeobs"
)

type panicHistoryExporter struct{}

func (panicHistoryExporter) Export(context.Context, runtimeobs.ExportRecord) error { return nil }
func (panicHistoryExporter) Close(context.Context) error                           { panic("close") }

var defaultHistory = processconfig.RuntimeHistory{QueueCapacity: 256, Timeout: 2 * time.Second, SampleInterval: 30 * time.Second}

func TestRuntimeHistoryUsesCoreDatabaseByDefault(t *testing.T) {
	setup, err := runtimeHistory(t.Context(), pgunit.NewPool(nil), defaultHistory)
	if err != nil {
		t.Fatal(err)
	}
	if len(setup.Options) != 1 || setup.Exporter != nil || setup.Reader == nil || setup.Prune == nil {
		t.Fatal("default history requires extra deployment")
	}
	capabilities := setup.Reader.Capabilities()
	if !capabilities.Durable() || capabilities.Retention != 7*24*time.Hour {
		t.Fatalf("incorrect default capabilities: %+v", capabilities)
	}
	if setup.SampleInterval != 30*time.Second {
		t.Fatal("default cadence missing")
	}
}

func TestRuntimeHistoryOptionalExportAndSamplingConfiguration(t *testing.T) {
	config := defaultHistory
	config.Endpoint, config.Headers, config.SampleInterval = "https://collector.example.test/v1/metrics", map[string]string{"Authorization": "Bearer private"}, time.Minute
	setup, err := runtimeHistory(t.Context(), pgunit.NewPool(nil), config)
	if err != nil {
		t.Fatal(err)
	}
	defer closeRuntimeHistory(t.Context(), setup.Exporter)
	if len(setup.Options) != 2 || setup.SampleInterval != time.Minute || setup.Reader.Capabilities().MinimumStep != time.Minute {
		t.Fatal("export and local history are not independent")
	}
}

func TestRuntimeHistoryClosePanicIsIsolated(t *testing.T) {
	closeRuntimeHistory(t.Context(), panicHistoryExporter{})
}
