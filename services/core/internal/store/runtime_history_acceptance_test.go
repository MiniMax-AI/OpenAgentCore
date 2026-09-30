package store_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimehistory"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimehistory/postgresreader"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimeobs"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func historyCapabilities() runtimehistory.Capabilities {
	return runtimehistory.Capabilities{CollectionMode: runtimehistory.CollectionPeriodic, SampleInterval: 30 * time.Second,
		Retention: 7 * 24 * time.Hour, MinimumStep: 30 * time.Second, MaximumRange: 24 * time.Hour, MaximumPoints: 1000, MaximumSeries: 64, MaximumTotalPoints: 10000,
		Metrics: []runtimehistory.Metric{runtimehistory.MetricCPU, runtimehistory.MetricMemory, runtimehistory.MetricTokens}}
}
func historyBackend(t *testing.T, s *store.Store) *postgresreader.Reader {
	t.Helper()
	r, err := postgresreader.New(s, postgresreader.Config{Capabilities: historyCapabilities(), QueryTimeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func historyOwner(t *testing.T, s *store.Store) runtimehistory.Scope {
	t.Helper()
	tenant := uuid.NewString()
	session, err := s.CreateSession(t.Context(), tenant, store.CreateSessionInput{Creator: store.FixtureCreator(), Engine: "codex", IdempotencyKey: uuid.NewString(), Configuration: json.RawMessage(`{"agent":{"model":"fixture-model"},"environment":{"type":"openai_hosted","workspace_directory":"/workspace","capability_directories":[]}}`)})
	if err != nil {
		t.Fatal(err)
	}
	environment, err := s.GetSessionEnvironment(t.Context(), tenant, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	return runtimehistory.Scope{TenantID: tenant, SessionID: session.ID, EnvironmentID: environment.ID}
}
func historyRecord(scope runtimehistory.Scope, allocation string, started, at time.Time, cpu float64, input uint64) runtimeobs.ExportRecord {
	capacity := 2.0
	memory := uint64(512)
	return runtimeobs.ExportRecord{TenantID: scope.TenantID, SessionID: scope.SessionID, EnvironmentID: scope.EnvironmentID, AllocationID: allocation, Mode: runtimeobs.ModeManaged, ProviderType: "docker", Status: runtimeobs.StatusObserved, CollectionSource: runtimeobs.CollectionSourcePeriodic, ResolvedAt: at,
		Sample: &runtimeobs.Sample{ObservedAt: at, StartedAt: &started, CPUUsageSecondsTotal: &cpu, CPUCapacityCores: &capacity, MemoryUsageBytes: &memory}, TokenUsage: &runtimeobs.TokenUsage{InputTokens: input, OutputTokens: input / 2}}
}
func historyQuery(scope runtimehistory.Scope, start, end time.Time, points int) runtimehistory.Query {
	step := time.Duration((int64(end.Sub(start)/time.Second)+int64(points)-1)/int64(points)) * time.Second
	if step < 30*time.Second {
		step = 30 * time.Second
	}
	return runtimehistory.Query{Scope: scope, Start: start, End: end, Step: step, Retention: 7 * 24 * time.Hour, MaxPoints: points, MaximumSeries: 64, MaximumTotalPoints: 10000}
}

func TestPostgresRuntimeHistoryAcceptance(t *testing.T) {
	s, pool := store.NewTestStore(t)
	scope := historyOwner(t, s)
	foreign := historyOwner(t, s)
	reader := historyBackend(t, s)
	end := time.Now().UTC().Truncate(time.Second)
	start := end.Add(-3 * time.Minute)
	started := start.Add(-time.Minute + 123*time.Nanosecond)
	allocation := uuid.NewString()
	records := []runtimeobs.ExportRecord{
		historyRecord(scope, allocation, started, start.Add(5*time.Second), 0, 10),
		historyRecord(scope, allocation, started, start.Add(35*time.Second), 30, 20),
		historyRecord(scope, allocation, started, start.Add(95*time.Second), 90, 40),
	}
	unavailable := historyRecord(scope, allocation, started, start.Add(155*time.Second), 100, 50)
	unavailable.Status = runtimeobs.StatusUnavailable
	unavailable.Sample = nil
	records = append(records, unavailable)
	for _, record := range records {
		if err := reader.Export(t.Context(), record); err != nil {
			t.Fatal(err)
		}
	}
	// Retry is idempotent; on-read and mismatched owners do not enter periodic history.
	if err := reader.Export(t.Context(), records[0]); err != nil {
		t.Fatal(err)
	}
	onRead := historyRecord(scope, allocation, started, start.Add(65*time.Second), 60, 30)
	onRead.CollectionSource = runtimeobs.CollectionSourceOnRead
	if err := reader.Export(t.Context(), onRead); err != nil {
		t.Fatal(err)
	}
	forged := historyRecord(scope, allocation, started, start.Add(75*time.Second), 70, 35)
	forged.TenantID = foreign.TenantID
	if err := reader.Export(t.Context(), forged); err != nil {
		t.Fatal(err)
	}
	// Another valid scope shares timestamps and cannot leak into the authorized read.
	if err := reader.Export(t.Context(), historyRecord(foreign, uuid.NewString(), started, start.Add(65*time.Second), 999, 999)); err != nil {
		t.Fatal(err)
	}
	freshPool, err := pgxpool.NewWithConfig(t.Context(), pool.Config())
	if err != nil {
		t.Fatal(err)
	}
	defer freshPool.Close()
	reader = historyBackend(t, store.New(freshPool))
	q := historyQuery(scope, start, end, 6)
	result, err := reader.Query(t.Context(), q)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Coverage) != 4 || len(result.Series) != 1 || len(result.TokenUsage) != 4 {
		t.Fatalf("durable samples or gaps changed: %+v", result)
	}
	if !result.Series[0].StartedAt.Equal(started) {
		t.Fatal("nanosecond incarnation identity was truncated", result.Series[0].StartedAt, started)
	}
	if result.Coverage[3].UnavailableCount != 1 || result.TokenUsage[3].InputTokens != 50 {
		t.Fatal("resource failure lost canonical usage")
	}
	if result.Series[0].Points[1].CPUUtilizationRatio == nil || *result.Series[0].Points[1].CPUUtilizationRatio != 0.5 {
		t.Fatal("CPU delta changed", result.Series[0].Points)
	}
	response := runtimehistory.Response{Capabilities: historyCapabilities(), Scope: scope, Requested: runtimehistory.Range{Start: start, End: end, MaxPoints: 6}, Resolution: q.Step, GeneratedAt: result.GeneratedAt, Coverage: result.Coverage, Series: result.Series, TokenUsage: result.TokenUsage}
	if err := response.Validate(time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []runtimehistory.Scope{{TenantID: foreign.TenantID, SessionID: scope.SessionID, EnvironmentID: scope.EnvironmentID}, {TenantID: scope.TenantID, SessionID: foreign.SessionID, EnvironmentID: scope.EnvironmentID}, {TenantID: scope.TenantID, SessionID: scope.SessionID, EnvironmentID: foreign.EnvironmentID}} {
		got, err := reader.Query(t.Context(), historyQuery(bad, start, end, 6))
		if err != nil || len(got.Coverage) != 0 || len(got.TokenUsage) != 0 {
			t.Fatal("scope isolation failed", got, err)
		}
	}
	// Chart snapshots do not alter canonical Session Usage.
	session, err := s.GetSession(t.Context(), scope.TenantID, scope.SessionID)
	if err != nil || len(session.Usage) != 0 && string(session.Usage) != "null" {
		t.Fatal("telemetry became accounting authority", string(session.Usage), err)
	}
}

func TestPostgresRuntimeHistoryDenseReadAndBoundedRetention(t *testing.T) {
	s, pool := store.NewTestStore(t)
	scope := historyOwner(t, s)
	reader := historyBackend(t, s)
	end := time.Now().UTC().Truncate(time.Second)
	start := end.Add(-24 * time.Hour)
	started := start.Add(-time.Hour + 987*time.Nanosecond)
	base := historyRecord(scope, uuid.NewString(), started, start, 0, 10)
	if err := reader.Export(t.Context(), base); err != nil {
		t.Fatal(err)
	}
	// Seed the equivalent of a full day of five-second periodic samples in one
	// fixture statement; production inserts still go through the exporter.
	_, err := pool.Exec(t.Context(), `INSERT INTO runtime_history_samples
 (tenant_id,session_id,environment_id,resolved_at_ns,allocation_id,provider_type,status,observed_at_ns,started_at_ns,cpu_usage_seconds,cpu_capacity_cores,memory_usage_bytes,input_tokens,output_tokens)
 SELECT tenant_id,session_id,environment_id,resolved_at_ns+n*5000000000,allocation_id,provider_type,status,observed_at_ns+n*5000000000,started_at_ns,n::double precision,cpu_capacity_cores,memory_usage_bytes,input_tokens+n,output_tokens+n
 FROM runtime_history_samples CROSS JOIN generate_series(1,17279) n WHERE tenant_id=$1 AND session_id=$2`, scope.TenantID, scope.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	for _, points := range []int{2, 60, 1000} {
		got, err := reader.Query(t.Context(), historyQuery(scope, start, end, points))
		if err != nil {
			t.Fatal(err)
		}
		count := 0
		for _, point := range got.Coverage {
			count += point.ObservationCount
		}
		if count != 17280 || len(got.Coverage) > points || len(got.TokenUsage) != len(got.Coverage) {
			t.Fatal("raw sample count tied to chart budget", count, len(got.Coverage), points)
		}
	}
	// Expired rows remain invisible before physical pruning runs.
	old := historyRecord(scope, base.AllocationID, started.Add(-8*24*time.Hour), start.Add(-8*24*time.Hour), 0, 1)
	if err := s.InsertRuntimeHistorySample(t.Context(), old); err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(t.Context(), `INSERT INTO runtime_history_samples
 (tenant_id,session_id,environment_id,resolved_at_ns,allocation_id,provider_type,status,observed_at_ns,started_at_ns)
 SELECT tenant_id,session_id,environment_id,resolved_at_ns-n,allocation_id,provider_type,'unavailable',NULL,NULL
 FROM runtime_history_samples CROSS JOIN generate_series(1,4100) n WHERE tenant_id=$1 AND session_id=$2 AND resolved_at_ns=$3`, scope.TenantID, scope.SessionID, old.ResolvedAt.UnixNano())
	if err != nil {
		t.Fatal(err)
	}
	got, err := reader.Query(t.Context(), historyQuery(scope, old.ResolvedAt.Add(-time.Second), old.ResolvedAt.Add(time.Minute), 3))
	if err != nil || len(got.Coverage) != 0 {
		t.Fatal("expired samples visible", got, err)
	}
	if _, err := reader.Prune(t.Context()); err != nil {
		t.Fatal(err)
	}
	var remaining int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM runtime_history_samples WHERE tenant_id=$1 AND resolved_at_ns<$2`, scope.TenantID, end.Add(-7*24*time.Hour).UnixNano()).Scan(&remaining); err != nil || remaining != 5 {
		t.Fatal("cleanup exceeded bounded batch", remaining, err)
	}
	if _, err := reader.Prune(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM runtime_history_samples WHERE tenant_id=$1 AND resolved_at_ns<$2`, scope.TenantID, end.Add(-7*24*time.Hour).UnixNano()).Scan(&remaining); err != nil || remaining != 0 {
		t.Fatal("retention cleanup incomplete", remaining, err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := reader.Query(cancelled, historyQuery(scope, start, end, 60)); err == nil {
		t.Fatal("cancelled query succeeded")
	}
}

func TestPostgresRuntimeHistoryKeepsProviderReportedUtilization(t *testing.T) {
	s, _ := store.NewTestStore(t)
	scope := historyOwner(t, s)
	reader := historyBackend(t, s)
	allocation := uuid.NewString()
	start := time.Now().UTC().Truncate(time.Minute).Add(-10 * time.Minute)
	started := start.Add(-time.Minute)
	for index, ratio := range []float64{.2, .4} {
		record := historyRecord(scope, allocation, started, start.Add(time.Duration(5+index*15)*time.Second), 0, 1)
		record.ProviderType = "e2b"
		record.Sample.CPUUsageSecondsTotal, record.Sample.CPUUtilizationRatio = nil, &ratio
		if err := reader.Export(t.Context(), record); err != nil {
			t.Fatal(err)
		}
	}
	result, err := reader.Query(t.Context(), historyQuery(scope, start, start.Add(time.Minute), 2))
	if err != nil || len(result.Series) != 1 || result.Series[0].Points[0].CPUUtilizationRatio == nil {
		t.Fatalf("E2B utilization was not retained: %+v %v", result, err)
	}
	if ratio := *result.Series[0].Points[0].CPUUtilizationRatio; ratio < .2999 || ratio > .3001 {
		t.Fatalf("bucket utilization = %v, want the mean of reported ratios", ratio)
	}
}
