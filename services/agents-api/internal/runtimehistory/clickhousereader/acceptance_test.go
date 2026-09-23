package clickhousereader

import (
	"context"
	"math"
	"os"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/runtimehistory"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/runtimeobs"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/runtimeobs/otlpexporter"
	"github.com/google/uuid"
)

func TestClickHouseCollectorAcceptance(t *testing.T) {
	address := os.Getenv("PARSAR_RUNTIME_HISTORY_ACCEPTANCE_CLICKHOUSE_ADDRESS")
	otlpEndpoint := os.Getenv("PARSAR_RUNTIME_HISTORY_ACCEPTANCE_OTLP_ENDPOINT")
	if address == "" || otlpEndpoint == "" {
		t.Skip("real ClickHouse/Collector acceptance is not configured")
	}
	database := envOr("PARSAR_RUNTIME_HISTORY_ACCEPTANCE_CLICKHOUSE_DATABASE", "runtime_history")
	username := envOr("PARSAR_RUNTIME_HISTORY_ACCEPTANCE_CLICKHOUSE_USERNAME", "default")
	password := os.Getenv("PARSAR_RUNTIME_HISTORY_ACCEPTANCE_CLICKHOUSE_PASSWORD")

	exporter, err := otlpexporter.New(t.Context(), otlpexporter.Config{
		Endpoint: otlpEndpoint, Insecure: true, RequestTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = exporter.Close(ctx)
	})

	end := time.Now().UTC().Truncate(time.Second)
	start := end.Add(-3 * time.Minute)
	tenantID := uuid.NewString()
	otherTenantID := uuid.NewString()
	sessionID := uuid.NewString()
	environmentID := uuid.NewString()
	allocationID := uuid.NewString()
	firstStartedAt := start.Add(5*time.Second + 123*time.Nanosecond)
	secondStartedAt := start.Add(95*time.Second + 456*time.Nanosecond)
	onRead := acceptanceObservedRecord(tenantID, sessionID, environmentID, allocationID, firstStartedAt, start.Add(80*time.Second), 120, 777)
	onRead.CollectionSource = runtimeobs.CollectionSourceOnRead

	records := []runtimeobs.ExportRecord{
		acceptanceObservedRecord(tenantID, sessionID, environmentID, allocationID, firstStartedAt, start.Add(30*time.Second), 0, 100),
		acceptanceObservedRecord(tenantID, sessionID, environmentID, allocationID, firstStartedAt, start.Add(60*time.Second), 30, 120),
		acceptanceObservedRecord(tenantID, sessionID, environmentID, allocationID, secondStartedAt, start.Add(110*time.Second), 0, 200),
		acceptanceObservedRecord(tenantID, sessionID, environmentID, allocationID, secondStartedAt, start.Add(140*time.Second), 10, 240),
		{
			TenantID: tenantID, SessionID: sessionID, EnvironmentID: environmentID,
			Mode: runtimeobs.ModeManaged, ProviderType: "docker", Status: runtimeobs.StatusUnavailable, Reason: "sample_timeout",
			CollectionSource: runtimeobs.CollectionSourcePeriodic, ResolvedAt: start.Add(155 * time.Second),
		},
		onRead,
		acceptanceObservedRecord(otherTenantID, sessionID, environmentID, allocationID, firstStartedAt, start.Add(70*time.Second), 90, 999),
	}
	for _, record := range records {
		if err := exporter.Export(t.Context(), record); err != nil {
			t.Fatal(err)
		}
	}

	capabilities := testCapabilities()
	open := func() *Reader {
		reader, err := Open(t.Context(), Config{
			Address: address, Database: database, Username: username, Password: password, Insecure: true,
			DialTimeout: 5 * time.Second, QueryTimeout: 5 * time.Second, Capabilities: capabilities,
		})
		if err != nil {
			t.Fatal(err)
		}
		return reader
	}
	query := runtimehistory.Query{
		Scope: runtimehistory.Scope{TenantID: tenantID, SessionID: sessionID, EnvironmentID: environmentID},
		Start: start, End: end, Step: 30 * time.Second, Retention: capabilities.Retention,
		MaxPoints: 6, MaximumSeries: capabilities.MaximumSeries, MaximumTotalPoints: capabilities.MaximumTotalPoints,
	}
	reader := open()
	var result runtimehistory.Result
	deadline := time.Now().Add(20 * time.Second)
	for {
		result, err = reader.Query(t.Context(), query)
		if err == nil && coverageCount(result.Coverage) == 5 && len(result.Series) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("history did not arrive through Collector: coverage=%d series=%d err=%v", coverageCount(result.Coverage), len(result.Series), err)
		}
		time.Sleep(250 * time.Millisecond)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	assertAcceptanceResult(t, result, firstStartedAt)

	// A fresh Reader proves history is backend-owned, not process memory.
	reader = open()
	t.Cleanup(func() { _ = reader.Close() })
	reloaded, err := reader.Query(t.Context(), query)
	if err != nil {
		t.Fatal(err)
	}
	if coverageCount(reloaded.Coverage) != 5 || len(reloaded.Series) != 1 {
		t.Fatalf("history did not survive Reader restart: %+v", reloaded)
	}
}

func acceptanceObservedRecord(tenantID, sessionID, environmentID, allocationID string, startedAt, observedAt time.Time, cpuSeconds float64, memoryBytes uint64) runtimeobs.ExportRecord {
	capacity := 2.0
	limit := uint64(2048)
	return runtimeobs.ExportRecord{
		TenantID: tenantID, SessionID: sessionID, EnvironmentID: environmentID, AllocationID: allocationID,
		Mode: runtimeobs.ModeManaged, ProviderType: "docker", Status: runtimeobs.StatusObserved,
		CollectionSource: runtimeobs.CollectionSourcePeriodic, ResolvedAt: observedAt.Add(time.Second),
		Sample: &runtimeobs.Sample{
			ObservedAt: observedAt, StartedAt: &startedAt, CPUUsageSecondsTotal: &cpuSeconds, CPUCapacityCores: &capacity,
			MemoryUsageBytes: &memoryBytes, MemoryLimitBytes: &limit,
		},
	}
}

func coverageCount(values []runtimehistory.CoveragePoint) int {
	total := 0
	for _, value := range values {
		total += value.ObservationCount
	}
	return total
}

func assertAcceptanceResult(t *testing.T, result runtimehistory.Result, firstStartedAt time.Time) {
	t.Helper()
	if coverageCount(result.Coverage) != 5 {
		t.Fatalf("cross-tenant sample leaked into coverage: %+v", result.Coverage)
	}
	if len(result.Series) != 1 || !result.Series[0].StartedAt.Equal(firstStartedAt) {
		t.Fatalf("allocation history was split by compute start estimates: %+v", result.Series)
	}
	wantRatios := []float64{.5, 1.0 / 6.0}
	found := 0
	for _, point := range result.Series[0].Points {
		if point.CPUUtilizationRatio != nil {
			if found >= len(wantRatios) || math.Abs(*point.CPUUtilizationRatio-wantRatios[found]) > 1e-9 {
				t.Fatalf("unexpected CPU ratio after allocation counter reset: %+v", point)
			}
			found++
		}
	}
	if found != len(wantRatios) {
		t.Fatalf("allocation lost CPU segments across counter reset: %+v", result.Series[0])
	}
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
