package clickhousereader

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/runtimehistory"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/runtimeobs"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/runtimeobs/otlpexporter"
	"github.com/google/uuid"
)

const (
	testTenant      = "11111111-1111-4111-8111-111111111111"
	testSession     = "22222222-2222-4222-8222-222222222222"
	testEnvironment = "33333333-3333-4333-8333-333333333333"
	testAllocation  = "44444444-4444-4444-8444-444444444444"
)

type fakeClient struct {
	rows       rows
	rowsQueue  []rows
	err        error
	statement  string
	statements []string
	args       []any
	closed     bool
}

func (c *fakeClient) Query(_ context.Context, statement string, args ...any) (rows, error) {
	c.statement, c.args = statement, append([]any(nil), args...)
	c.statements = append(c.statements, statement)
	if len(c.rowsQueue) > 0 {
		value := c.rowsQueue[0]
		c.rowsQueue = c.rowsQueue[1:]
		return value, c.err
	}
	return c.rows, c.err
}

func (c *fakeClient) Close() error {
	c.closed = true
	return nil
}

type fakeRows struct {
	values [][]any
	index  int
	err    error
}

func (r *fakeRows) Next() bool { return r.index < len(r.values) }

func (r *fakeRows) Scan(destinations ...any) error {
	if r.index >= len(r.values) || len(destinations) != len(r.values[r.index]) {
		return errors.New("invalid fake scan")
	}
	for index, value := range r.values[r.index] {
		switch destination := destinations[index].(type) {
		case *int64:
			*destination = value.(int64)
		case *float64:
			*destination = value.(float64)
		case *string:
			*destination = value.(string)
		case sql.Scanner:
			if err := destination.Scan(value); err != nil {
				return err
			}
		default:
			return errors.New("unsupported fake scan destination")
		}
	}
	r.index++
	return nil
}

func (r *fakeRows) Err() error   { return r.err }
func (r *fakeRows) Close() error { return nil }

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

func metricRow(resolved, observed int64, allocation string, started *int64, provider, status, metric string, value float64) []any {
	var observedValue any = observed
	if observed < 0 {
		observedValue = nil
	}
	var startedValue any
	if started != nil {
		startedValue = *started
	}
	return []any{resolved, observedValue, allocation, startedValue, provider, status, metric, value, value}
}

func TestReaderQueriesMandatoryScopeAndAggregatesFencedHistory(t *testing.T) {
	start := time.Date(2026, 9, 23, 8, 0, 0, 0, time.UTC)
	started := start.Add(-time.Minute).UnixNano()
	baseline := start.Add(-30 * time.Second).UnixNano()
	observed := start.Add(10 * time.Second).UnixNano()
	unavailable := start.Add(40 * time.Second).UnixNano()
	client := &fakeClient{rows: &fakeRows{values: [][]any{
		metricRow(baseline, baseline, testAllocation, &started, "docker", "observed", otlpexporter.SampleName, 1),
		metricRow(baseline, baseline, testAllocation, &started, "docker", "observed", otlpexporter.CPUUsageName, 1),
		metricRow(baseline, baseline, testAllocation, &started, "docker", "observed", otlpexporter.CPUCapacityName, 2),
		metricRow(observed, observed, testAllocation, &started, "docker", "observed", otlpexporter.SampleName, 1),
		metricRow(observed, observed, testAllocation, &started, "docker", "observed", otlpexporter.CPUUsageName, 3),
		metricRow(observed, observed, testAllocation, &started, "docker", "observed", otlpexporter.CPUCapacityName, 2),
		metricRow(observed, observed, testAllocation, &started, "docker", "observed", otlpexporter.MemoryUsageName, 1024),
		metricRow(observed, observed, testAllocation, &started, "docker", "observed", otlpexporter.MemoryLimitName, 2048),
		metricRow(unavailable, -1, "", nil, "docker", "unavailable", otlpexporter.SampleName, 1),
	}}}
	reader, err := newReader(client, testCapabilities(), 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	reader.now = func() time.Time { return start.Add(time.Minute) }
	result, err := reader.Query(t.Context(), testQuery(start))
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"tenant_id = {tenant_id:String}", "session_id = {session_id:String}", "environment_id = {environment_id:String}",
		"collection_source = 'periodic'", "resolved_at_unix_nano >= {raw_start:Int64}", "resolved_at_unix_nano < {end:Int64}",
	} {
		if !strings.Contains(client.statement, required) {
			t.Fatalf("history query lacks mandatory predicate %q", required)
		}
	}
	if len(client.args) != 6 {
		t.Fatalf("unexpected bound argument count: %d", len(client.args))
	}
	if len(result.Coverage) != 2 || result.Coverage[0].ObservedCount != 1 || result.Coverage[1].UnavailableCount != 1 {
		t.Fatalf("unexpected coverage: %+v", result.Coverage)
	}
	if len(result.Series) != 1 || result.Series[0].AllocationID != testAllocation || !result.Series[0].StartedAt.Equal(time.Unix(0, started)) || len(result.Series[0].Points) != 1 {
		t.Fatalf("unexpected fenced series: %+v", result.Series)
	}
	point := result.Series[0].Points[0]
	if point.CPUUtilizationRatio == nil || *point.CPUUtilizationRatio != .025 || point.CPUCapacityCores == nil || *point.CPUCapacityCores != 2 ||
		point.MemoryUsageBytes == nil || *point.MemoryUsageBytes != 1024 || point.MemoryLimitBytes == nil || *point.MemoryLimitBytes != 2048 {
		t.Fatalf("unexpected metric aggregation: %+v", point)
	}
	if err := runtimehistoryResponseValidation(testQuery(start), result); err != nil {
		t.Fatal(err)
	}
}

func TestReaderReturnsSessionTokenUsageIndependentOfRuntimeIncarnation(t *testing.T) {
	start := time.Date(2026, 9, 23, 8, 0, 0, 0, time.UTC)
	first := start.Add(10 * time.Second).UnixNano()
	second := start.Add(40 * time.Second).UnixNano()
	client := &fakeClient{rowsQueue: []rows{
		&fakeRows{},
		&fakeRows{values: [][]any{
			{first, otlpexporter.TokenInputName, float64(100), float64(100)},
			{first, otlpexporter.TokenOutputName, float64(20), float64(20)},
			{second, otlpexporter.TokenInputName, float64(160), float64(160)},
			{second, otlpexporter.TokenOutputName, float64(50), float64(50)},
		}},
	}}
	capabilities := testCapabilities()
	capabilities.Metrics = append(capabilities.Metrics, runtimehistory.MetricTokens)
	reader, err := newReader(client, capabilities, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	reader.now = func() time.Time { return start.Add(time.Minute) }
	result, err := reader.Query(t.Context(), testQuery(start))
	if err != nil {
		t.Fatal(err)
	}
	if len(client.statements) != 2 || !strings.Contains(client.statements[1], "agents.session.tokens.input") {
		t.Fatalf("token query was not issued separately: %#v", client.statements)
	}
	if len(result.TokenUsage) != 2 || result.TokenUsage[0].InputTokens != 100 || result.TokenUsage[1].OutputTokens != 50 {
		t.Fatalf("unexpected token usage history: %+v", result.TokenUsage)
	}
}

func TestReaderRejectsIncompleteSessionTokenUsage(t *testing.T) {
	start := time.Date(2026, 9, 23, 8, 0, 0, 0, time.UTC)
	rows := &fakeRows{values: [][]any{{
		start.Add(10 * time.Second).UnixNano(), otlpexporter.TokenInputName, float64(100), float64(100),
	}}}
	if _, _, err := readTokenUsage(rows, start, start.Add(time.Minute)); err == nil {
		t.Fatal("incomplete token usage was accepted")
	}
}

func runtimehistoryResponseValidation(query runtimehistory.Query, result runtimehistory.Result) error {
	response := runtimehistory.Response{
		Capabilities: testCapabilities(), Scope: query.Scope,
		Requested: runtimehistory.Range{Start: query.Start, End: query.End, MaxPoints: query.MaxPoints}, Resolution: query.Step,
		GeneratedAt: result.GeneratedAt, RetainedFrom: result.RetainedFrom, Coverage: result.Coverage, Series: result.Series,
	}
	return response.Validate(result.GeneratedAt)
}

func TestReaderFailsClosedOnBackendAndConflictingRows(t *testing.T) {
	start := time.Date(2026, 9, 23, 8, 0, 0, 0, time.UTC)
	client := &fakeClient{err: errors.New("secret backend detail")}
	reader, err := newReader(client, testCapabilities(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	reader.now = func() time.Time { return start.Add(time.Minute) }
	if _, err := reader.Query(t.Context(), testQuery(start)); err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatalf("backend failure was not sanitized: %v", err)
	}

	resolved := start.Add(10 * time.Second).UnixNano()
	started := start.Add(-time.Minute).UnixNano()
	rows := metricRow(resolved, resolved, testAllocation, &started, "docker", "observed", otlpexporter.SampleName, 1)
	rows[len(rows)-1] = float64(2)
	client = &fakeClient{rows: &fakeRows{values: [][]any{rows}}}
	reader, err = newReader(client, testCapabilities(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	reader.now = func() time.Time { return start.Add(time.Minute) }
	if _, err := reader.Query(t.Context(), testQuery(start)); err == nil {
		t.Fatal("conflicting duplicate metric values were accepted")
	}
}

func TestReadSamplesRejectsMalformedProjectedRecords(t *testing.T) {
	start := time.Date(2026, 9, 23, 8, 0, 0, 0, time.UTC)
	resolved := start.Add(10 * time.Second).UnixNano()
	started := start.Add(-time.Minute).UnixNano()
	tests := []struct {
		name string
		rows [][]any
	}{
		{
			name: "observed marker without observed timestamp",
			rows: [][]any{metricRow(resolved, -1, testAllocation, nil, "docker", "observed", otlpexporter.SampleName, 1)},
		},
		{
			name: "unavailable marker with observed timestamp",
			rows: [][]any{metricRow(resolved, resolved, testAllocation, nil, "docker", "unavailable", otlpexporter.SampleName, 1)},
		},
		{
			name: "unsupported marker with incarnation timestamp",
			rows: [][]any{metricRow(resolved, -1, testAllocation, &started, "docker", "unsupported", otlpexporter.SampleName, 1)},
		},
		{
			name: "unavailable resource metric",
			rows: [][]any{metricRow(resolved, -1, testAllocation, nil, "docker", "unavailable", otlpexporter.MemoryUsageName, 1)},
		},
		{
			name: "observed resource metric without incarnation fence",
			rows: [][]any{metricRow(resolved, resolved, testAllocation, nil, "docker", "observed", otlpexporter.MemoryUsageName, 1)},
		},
		{
			name: "invalid sample marker value",
			rows: [][]any{metricRow(resolved, resolved, testAllocation, &started, "docker", "observed", otlpexporter.SampleName, 2)},
		},
		{
			name: "resource metrics without sample marker",
			rows: [][]any{metricRow(resolved, resolved, testAllocation, &started, "docker", "observed", otlpexporter.MemoryUsageName, 1)},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, _, err := readSamples(&fakeRows{values: test.rows}, start, start.Add(time.Minute)); err == nil {
				t.Fatal("malformed projection rows were accepted")
			}
		})
	}
}

func TestReaderRejectsUnscopedQueriesAndClosesClient(t *testing.T) {
	client := &fakeClient{rows: &fakeRows{}}
	reader, err := newReader(client, testCapabilities(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	query := testQuery(time.Now().UTC().Truncate(time.Second).Add(-time.Minute))
	query.TenantID = ""
	if _, err := reader.Query(t.Context(), query); err == nil || client.statement != "" {
		t.Fatal("unscoped query reached ClickHouse")
	}
	if err := reader.Close(); err != nil || !client.closed {
		t.Fatal("Reader did not close ClickHouse client")
	}
}

func TestOpenRequiresExplicitClickHouseTransportSecurity(t *testing.T) {
	config := Config{
		Address: "clickhouse.example.test:9000", Database: "runtime_history", Username: "reader", Password: "must-not-leak",
		DialTimeout: time.Second, QueryTimeout: time.Second, Capabilities: testCapabilities(),
	}
	if _, err := Open(t.Context(), config); err == nil || strings.Contains(err.Error(), "must-not-leak") {
		t.Fatalf("implicit plaintext ClickHouse configuration was accepted or leaked: %v", err)
	}
	config.Insecure = true
	reader, err := Open(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestAggregateSeriesUsesProviderObservationOrder(t *testing.T) {
	start := time.Date(2026, 9, 23, 8, 0, 0, 0, time.UTC)
	query := testQuery(start)
	startedAt := start.Add(-time.Minute)
	firstObserved, secondObserved := start.Add(5*time.Second), start.Add(15*time.Second)
	firstResolved, secondResolved := start.Add(20*time.Second), start.Add(10*time.Second)
	samples := []*rawSample{
		{
			resolvedAt: secondResolved, observedAt: &secondObserved, allocation: testAllocation, startedAt: &startedAt,
			provider: "docker", status: runtimeobs.StatusObserved, hasSample: true,
			metrics: map[string]float64{otlpexporter.SampleName: 1, otlpexporter.CPUUsageName: 3, otlpexporter.CPUCapacityName: 2, otlpexporter.MemoryUsageName: 200},
		},
		{
			resolvedAt: firstResolved, observedAt: &firstObserved, allocation: testAllocation, startedAt: &startedAt,
			provider: "docker", status: runtimeobs.StatusObserved, hasSample: true,
			metrics: map[string]float64{otlpexporter.SampleName: 1, otlpexporter.CPUUsageName: 1, otlpexporter.CPUCapacityName: 2, otlpexporter.MemoryUsageName: 100},
		},
	}
	points, err := aggregateSeries(query, samples)
	if err != nil {
		t.Fatal(err)
	}
	if len(points) != 1 || points[0].CPUUtilizationRatio == nil || *points[0].CPUUtilizationRatio != .1 || points[0].MemoryUsageBytes == nil || *points[0].MemoryUsageBytes != 200 {
		t.Fatalf("provider observation order was not preserved: %+v", points)
	}
}

func TestAggregateSeriesLeavesCPUUsageGapWhenEitherEndpointLacksCapacity(t *testing.T) {
	start := time.Date(2026, 9, 23, 8, 0, 0, 0, time.UTC)
	query := testQuery(start)
	startedAt := start.Add(-time.Minute)
	firstObserved, secondObserved, thirdObserved := start.Add(-5*time.Second), start.Add(5*time.Second), start.Add(15*time.Second)
	samples := []*rawSample{
		{
			resolvedAt: firstObserved, observedAt: &firstObserved, allocation: testAllocation, startedAt: &startedAt,
			provider: "docker", status: runtimeobs.StatusObserved, hasSample: true,
			metrics: map[string]float64{otlpexporter.SampleName: 1, otlpexporter.CPUUsageName: 1, otlpexporter.CPUCapacityName: 2},
		},
		{
			resolvedAt: secondObserved, observedAt: &secondObserved, allocation: testAllocation, startedAt: &startedAt,
			provider: "docker", status: runtimeobs.StatusObserved, hasSample: true,
			metrics: map[string]float64{otlpexporter.SampleName: 1, otlpexporter.CPUUsageName: 3},
		},
		{
			resolvedAt: thirdObserved, observedAt: &thirdObserved, allocation: testAllocation, startedAt: &startedAt,
			provider: "docker", status: runtimeobs.StatusObserved, hasSample: true,
			metrics: map[string]float64{otlpexporter.SampleName: 1, otlpexporter.CPUUsageName: 5, otlpexporter.CPUCapacityName: 2},
		},
	}
	points, err := aggregateSeries(query, samples)
	if err != nil {
		t.Fatal(err)
	}
	if len(points) != 1 || points[0].CPUUtilizationRatio != nil {
		t.Fatalf("missing CPU capacity was synthesized into utilization: %+v", points)
	}
}

func TestAggregateIgnoresLookbackOnlySeriesForSeriesLimit(t *testing.T) {
	start := time.Date(2026, 9, 23, 8, 0, 0, 0, time.UTC)
	query := testQuery(start)
	query.MaximumSeries = 64
	raw := make([]*rawSample, 0, 66)
	for range 65 {
		startedAt := start.Add(-time.Minute)
		observedAt := start.Add(-2 * time.Second)
		raw = append(raw, &rawSample{
			resolvedAt: start.Add(-time.Second), observedAt: &observedAt, allocation: uuid.NewString(), startedAt: &startedAt,
			provider: "docker", status: runtimeobs.StatusObserved, hasSample: true, metrics: map[string]float64{otlpexporter.SampleName: 1},
		})
	}
	startedAt := start.Add(-time.Minute)
	observedAt := start.Add(5 * time.Second)
	raw = append(raw, &rawSample{
		resolvedAt: start.Add(6 * time.Second), observedAt: &observedAt, allocation: uuid.NewString(), startedAt: &startedAt,
		provider: "docker", status: runtimeobs.StatusObserved, hasSample: true, metrics: map[string]float64{otlpexporter.SampleName: 1},
	})
	result, err := aggregate(query, start.Add(time.Minute), raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Series) != 1 || len(result.Series[0].Points) != 1 {
		t.Fatalf("lookback-only incarnations consumed returned-series budget: %+v", result.Series)
	}
}
