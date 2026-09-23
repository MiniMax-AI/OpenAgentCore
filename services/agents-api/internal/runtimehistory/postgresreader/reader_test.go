package postgresreader

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/runtimehistory"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/runtimeobs"
)

const (
	testTenant      = "11111111-1111-4111-8111-111111111111"
	testSession     = "22222222-2222-4222-8222-222222222222"
	testEnvironment = "33333333-3333-4333-8333-333333333333"
	testAllocation  = "44444444-4444-4444-8444-444444444444"
)

func testCapabilities() runtimehistory.Capabilities {
	return runtimehistory.Capabilities{
		CollectionMode: runtimehistory.CollectionPeriodic, SampleInterval: 30 * time.Second,
		Retention: 7 * 24 * time.Hour, MinimumStep: 30 * time.Second, MaximumRange: 24 * time.Hour,
		MaximumPoints: 1_000, MaximumSeries: 64, MaximumTotalPoints: 10_000,
		Metrics: []runtimehistory.Metric{runtimehistory.MetricCPU, runtimehistory.MetricMemory},
	}
}

func testQuery(start time.Time) runtimehistory.Query {
	return runtimehistory.Query{
		Scope: runtimehistory.Scope{TenantID: testTenant, SessionID: testSession, EnvironmentID: testEnvironment},
		Start: start, End: start.Add(time.Minute), Step: 30 * time.Second, Retention: 7 * 24 * time.Hour,
		MaxPoints: 2, MaximumSeries: 64, MaximumTotalPoints: 10_000,
	}
}

type fakeStore struct {
	records    []runtimeobs.ExportRecord
	written    []runtimeobs.ExportRecord
	err        error
	limit      int32
	scope      runtimehistory.Scope
	start, end int64
	pruneCalls int
	pruneCount int64
}

func (s *fakeStore) InsertRuntimeHistorySample(_ context.Context, r runtimeobs.ExportRecord) error {
	s.written = append(s.written, r)
	return s.err
}
func (s *fakeStore) ListRuntimeHistorySamples(_ context.Context, tenant, session, environment string, start, end int64, limit int32) ([]runtimeobs.ExportRecord, error) {
	s.scope = runtimehistory.Scope{TenantID: tenant, SessionID: session, EnvironmentID: environment}
	s.start = start
	s.end = end
	s.limit = limit
	return s.records, s.err
}
func (s *fakeStore) PruneRuntimeHistorySamples(context.Context, int64) (int64, error) {
	s.pruneCalls++
	return s.pruneCount, s.err
}
func testReader(t *testing.T, s *fakeStore, now time.Time) *Reader {
	t.Helper()
	caps := testCapabilities()
	caps.Metrics = append(caps.Metrics, runtimehistory.MetricTokens)
	r, err := New(s, Config{Capabilities: caps, QueryTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	r.now = func() time.Time { return now }
	return r
}
func observedRecord(at, started time.Time, cpu float64) runtimeobs.ExportRecord {
	capacity := 2.0
	memory := uint64(123)
	return runtimeobs.ExportRecord{TenantID: testTenant, SessionID: testSession, EnvironmentID: testEnvironment, AllocationID: testAllocation,
		Mode: runtimeobs.ModeManaged, ProviderType: "docker", Status: runtimeobs.StatusObserved, CollectionSource: runtimeobs.CollectionSourcePeriodic,
		ResolvedAt: at, Sample: &runtimeobs.Sample{ObservedAt: at, StartedAt: &started, CPUUsageSecondsTotal: &cpu, CPUCapacityCores: &capacity, MemoryUsageBytes: &memory},
		TokenUsage: &runtimeobs.TokenUsage{InputTokens: 12, OutputTokens: 3}}
}
func TestReaderDenseInputBudgetIndependentOfChartPoints(t *testing.T) {
	end := time.Now().UTC().Truncate(time.Second)
	start := end.Add(-24 * time.Hour)
	started := start.Add(-time.Hour)
	s := &fakeStore{}
	for at := start; at.Before(end); at = at.Add(5 * time.Second) {
		s.records = append(s.records, observedRecord(at, started, at.Sub(start).Seconds()))
	}
	r := testReader(t, s, end)
	for _, points := range []int{2, 60, 1000} {
		q := testQuery(start)
		q.End = end
		q.MaxPoints = points
		q.Step = time.Duration((86400+points-1)/points) * time.Second
		result, err := r.Query(t.Context(), q)
		if err != nil {
			t.Fatal(err)
		}
		if s.limit != maximumRawSamples+1 || s.scope != q.Scope || s.start != start.Add(-time.Minute).UnixNano() {
			t.Fatal("read bounds changed", s)
		}
		count := 0
		for _, point := range result.Coverage {
			count += point.ObservationCount
		}
		if count != 17280 || len(result.Series) != 1 || len(result.TokenUsage) != len(result.Coverage) || len(result.Coverage) > points {
			t.Fatal("dense downsampling dropped samples", count, len(result.Coverage))
		}
	}
	s.records = make([]runtimeobs.ExportRecord, maximumRawSamples+1)
	if _, err := r.Query(t.Context(), testQuery(start)); err == nil {
		t.Fatal("raw overflow accepted")
	}
}
func TestReaderFailsClosedAndScopesEveryRead(t *testing.T) {
	start := time.Now().UTC().Truncate(time.Second).Add(-time.Minute)
	s := &fakeStore{err: errors.New("private backend address")}
	r := testReader(t, s, start.Add(time.Minute))
	if _, err := r.Query(t.Context(), testQuery(start)); err == nil || strings.Contains(err.Error(), "private") {
		t.Fatal(err)
	}
	s.err = nil
	s.records = []runtimeobs.ExportRecord{observedRecord(start, start.Add(-time.Minute), 0)}
	s.records[0].TenantID = testSession
	if _, err := r.Query(t.Context(), testQuery(start)); err == nil {
		t.Fatal("foreign row accepted")
	}
	q := testQuery(start)
	q.EnvironmentID = ""
	if _, err := r.Query(t.Context(), q); err == nil {
		t.Fatal("unscoped query accepted")
	}
}
func TestExporterPersistsOnlyPeriodicAndPreservesUnknownMetrics(t *testing.T) {
	now := time.Now().UTC()
	s := &fakeStore{}
	r := testReader(t, s, now)
	record := observedRecord(now.Add(-time.Second), now.Add(-time.Hour), 0)
	record.CollectionSource = runtimeobs.CollectionSourceOnRead
	if err := r.Export(t.Context(), record); err != nil || len(s.written) != 0 {
		t.Fatal(err)
	}
	record.CollectionSource = runtimeobs.CollectionSourcePeriodic
	record.Sample.StartedAt = nil
	if err := r.Export(t.Context(), record); err != nil {
		t.Fatal(err)
	}
	if len(s.written) != 1 || s.written[0].Sample.CPUUsageSecondsTotal != nil || s.written[0].TokenUsage.InputTokens != 12 {
		t.Fatal(s.written)
	}
	record.Sample = nil
	record.Status = runtimeobs.StatusUnavailable
	if err := r.Export(t.Context(), record); err != nil {
		t.Fatal(err)
	}
	if s.written[1].Sample != nil || s.written[1].TokenUsage == nil {
		t.Fatal("unavailable observation lost canonical usage")
	}
	record.ResolvedAt = now.Add(-retention - time.Second)
	if err := r.Export(t.Context(), record); err != nil || len(s.written) != 2 {
		t.Fatal("expired record persisted", err)
	}
}
func TestPruneHasBoundedWorkAndSanitizedFailure(t *testing.T) {
	s := &fakeStore{pruneCount: 256}
	r := testReader(t, s, time.Now())
	if err := r.Prune(t.Context()); err != nil || s.pruneCalls != 16 {
		t.Fatal(err, s.pruneCalls)
	}
	s.err = errors.New("secret")
	if err := r.Prune(t.Context()); err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatal(err)
	}
}
func TestRestartFencesCPUWithoutSplittingAllocation(t *testing.T) {
	start := time.Now().UTC().Truncate(time.Second).Add(-time.Minute)
	firstStart := start.Add(-time.Hour)
	nextStart := start.Add(15 * time.Second)
	s := &fakeStore{records: []runtimeobs.ExportRecord{observedRecord(start, firstStart, 1), observedRecord(start.Add(10*time.Second), firstStart, 11), observedRecord(start.Add(30*time.Second), nextStart, 100), observedRecord(start.Add(40*time.Second), nextStart, 110)}}
	r := testReader(t, s, start.Add(time.Minute))
	result, err := r.Query(t.Context(), testQuery(start))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Series) != 1 || !result.Series[0].StartedAt.Equal(firstStart) || len(result.Series[0].Points) != 2 {
		t.Fatal(result)
	}
	for _, point := range result.Series[0].Points {
		if point.CPUUtilizationRatio == nil || *point.CPUUtilizationRatio != 0.5 {
			t.Fatal("CPU bridged native restart", point)
		}
	}
}

func TestUnavailableObservationBreaksCPUContinuity(t *testing.T) {
	start := time.Now().UTC().Truncate(time.Second).Add(-time.Minute)
	started := start.Add(-time.Hour)
	unavailable := observedRecord(start.Add(20*time.Second), started, 0)
	unavailable.Sample = nil
	unavailable.Status = runtimeobs.StatusUnavailable
	s := &fakeStore{records: []runtimeobs.ExportRecord{observedRecord(start.Add(5*time.Second), started, 1), unavailable, observedRecord(start.Add(35*time.Second), started, 100)}}
	r := testReader(t, s, start.Add(time.Minute))
	result, err := r.Query(t.Context(), testQuery(start))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Series) != 1 || len(result.Series[0].Points) != 2 || result.Series[0].Points[0].UnavailableCount != 1 {
		t.Fatal("outage missing from allocation coverage", result)
	}
	for _, point := range result.Series[0].Points {
		if point.CPUUtilizationRatio != nil {
			t.Fatal("CPU bridged unavailable sample", point)
		}
	}
}
