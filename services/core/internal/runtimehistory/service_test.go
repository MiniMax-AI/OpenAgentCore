package runtimehistory

import (
	"context"
	"errors"
	"testing"
	"time"
)

const (
	tenantID      = "11111111-1111-4111-8111-111111111111"
	sessionID     = "22222222-2222-4222-8222-222222222222"
	environmentID = "33333333-3333-4333-8333-333333333333"
	allocationID  = "44444444-4444-4444-8444-444444444444"
)

type fixedScopeResolver struct {
	scope Scope
	err   error
}

func (r fixedScopeResolver) ResolveRuntimeHistoryScope(context.Context, string, string) (Scope, error) {
	return r.scope, r.err
}

type fakeReader struct {
	capabilities Capabilities
	result       Result
	err          error
	queries      []Query
}

func (r *fakeReader) Capabilities() Capabilities { return r.capabilities }

func (r *fakeReader) Query(_ context.Context, query Query) (Result, error) {
	r.queries = append(r.queries, query)
	return r.result, r.err
}

func capabilities() Capabilities {
	return Capabilities{
		CollectionMode:     CollectionPeriodic,
		SampleInterval:     30 * time.Second,
		Retention:          7 * 24 * time.Hour,
		MinimumStep:        30 * time.Second,
		MaximumRange:       24 * time.Hour,
		MaximumPoints:      1_000,
		MaximumSeries:      64,
		MaximumTotalPoints: 10_000,
		Metrics:            []Metric{MetricCPU, MetricMemory, MetricTokens},
	}
}

func TestServiceAuthorizesAndBoundsBackendQuery(t *testing.T) {
	now := time.Date(2026, 9, 23, 8, 0, 0, 0, time.UTC)
	start := now.Add(-time.Hour)
	startedAt := start.Add(-time.Minute)
	ratio, capacity := .25, 2.0
	memory, limit := uint64(1024), uint64(2048)
	scope := Scope{TenantID: tenantID, SessionID: sessionID, EnvironmentID: environmentID}
	reader := &fakeReader{capabilities: capabilities()}
	reader.result = Result{GeneratedAt: now, RetainedFrom: &start, TokenUsage: []TokenUsagePoint{{
		Start: start, End: start.Add(30 * time.Second), SampledAt: start.Add(20 * time.Second), InputTokens: 120, OutputTokens: 30,
	}}, Series: []Series{{
		Scope: scope, AllocationID: allocationID, StartedAt: startedAt, ProviderType: "docker",
		Points: []Point{{
			Start: start, End: start.Add(30 * time.Second), FirstObservedAt: start.Add(time.Second), LastObservedAt: start.Add(20 * time.Second),
			ObservationCount: 2, ObservedCount: 2, CPUContributorCount: 2, MemoryContributorCount: 2,
			CPUUtilizationRatio: &ratio, CPUCapacityCores: &capacity, MemoryUsageBytes: &memory, MemoryLimitBytes: &limit,
		}},
	}}}
	service, err := NewService(fixedScopeResolver{scope: scope}, reader)
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return now }
	response, err := service.QuerySession(t.Context(), tenantID, sessionID, Range{Start: start, End: now, MaxPoints: 60})
	if err != nil {
		t.Fatal(err)
	}
	if len(reader.queries) != 1 || reader.queries[0].Scope != scope || reader.queries[0].Step != time.Minute || response.Resolution != time.Minute || !response.Durable() {
		t.Fatalf("unexpected bounded history query: query=%+v response=%+v", reader.queries, response)
	}
	ratio = .9
	if response.Series[0].Points[0].CPUUtilizationRatio == nil || *response.Series[0].Points[0].CPUUtilizationRatio != .25 {
		t.Fatal("response aliases backend-owned metric memory")
	}
	reader.result.TokenUsage[0].InputTokens = 999
	if response.TokenUsage[0].InputTokens != 120 {
		t.Fatal("response aliases backend-owned token usage memory")
	}
}

func TestServiceNeverQueriesBeforeOwnershipResolution(t *testing.T) {
	reader := &fakeReader{capabilities: capabilities()}
	denied := errors.New("not found")
	service, err := NewService(fixedScopeResolver{err: denied}, reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	_, err = service.QuerySession(t.Context(), tenantID, sessionID, Range{Start: now.Add(-time.Hour), End: now, MaxPoints: 60})
	if !errors.Is(err, denied) || len(reader.queries) != 0 {
		t.Fatalf("unauthorized history reached reader: err=%v queries=%+v", err, reader.queries)
	}
}

func TestServiceValidatesResultAgainstTimeAfterReaderReturns(t *testing.T) {
	requestNow := time.Date(2026, 9, 23, 8, 0, 0, 0, time.UTC)
	resultNow := requestNow.Add(2 * time.Second)
	scope := Scope{TenantID: tenantID, SessionID: sessionID, EnvironmentID: environmentID}
	reader := &fakeReader{capabilities: capabilities(), result: Result{GeneratedAt: resultNow}}
	service, err := NewService(fixedScopeResolver{scope: scope}, reader)
	if err != nil {
		t.Fatal(err)
	}
	clockCalls := 0
	service.now = func() time.Time {
		clockCalls++
		if clockCalls == 1 {
			return requestNow
		}
		return resultNow
	}

	_, err = service.QuerySession(t.Context(), tenantID, sessionID, Range{
		Start: requestNow.Add(-time.Hour), End: requestNow, MaxPoints: 60,
	})
	if err != nil {
		t.Fatalf("valid result from slow Reader rejected: %v", err)
	}
	if clockCalls != 2 {
		t.Fatalf("clock calls = %d, want request and result validation times", clockCalls)
	}
}

func TestServiceRejectsInvalidRangeAndResolverIdentity(t *testing.T) {
	reader := &fakeReader{capabilities: capabilities()}
	scope := Scope{TenantID: tenantID, SessionID: sessionID, EnvironmentID: environmentID}
	service, err := NewService(fixedScopeResolver{scope: scope}, reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	for _, requested := range []Range{
		{Start: now, End: now, MaxPoints: 60},
		{Start: now.Add(-25 * time.Hour), End: now, MaxPoints: 60},
		{Start: now.Add(-time.Hour), End: now, MaxPoints: 1},
		{Start: now.Add(-time.Hour), End: now, MaxPoints: 1_001},
	} {
		if _, err := service.QuerySession(t.Context(), tenantID, sessionID, requested); err == nil {
			t.Fatalf("invalid range accepted: %+v", requested)
		}
	}
	if len(reader.queries) != 0 {
		t.Fatalf("invalid range reached reader: %+v", reader.queries)
	}
	service.resolver = fixedScopeResolver{scope: Scope{TenantID: "55555555-5555-4555-8555-555555555555", SessionID: sessionID, EnvironmentID: environmentID}}
	if _, err := service.QuerySession(t.Context(), tenantID, sessionID, Range{Start: now.Add(-time.Hour), End: now, MaxPoints: 60}); err == nil || len(reader.queries) != 0 {
		t.Fatal("mismatched resolver identity reached reader")
	}
}

func TestServiceRejectsMalformedBackendResults(t *testing.T) {
	now := time.Date(2026, 9, 23, 8, 0, 0, 0, time.UTC)
	start := now.Add(-time.Hour)
	scope := Scope{TenantID: tenantID, SessionID: sessionID, EnvironmentID: environmentID}
	base := Series{Scope: scope, AllocationID: allocationID, StartedAt: start.Add(-time.Minute), ProviderType: "docker"}
	for name, mutate := range map[string]func(*Result){
		"cross tenant":            func(result *Result) { result.Series[0].TenantID = "55555555-5555-4555-8555-555555555555" },
		"noncanonical allocation": func(result *Result) { result.Series[0].AllocationID = "urn:uuid:" + allocationID },
		"nil allocation":          func(result *Result) { result.Series[0].AllocationID = "00000000-0000-0000-0000-000000000000" },
		"generation before range": func(result *Result) { result.GeneratedAt = start.Add(-time.Second) },
		"pre-epoch generation":    func(result *Result) { result.GeneratedAt = time.Unix(-1, 0) },
		"pre-epoch incarnation":   func(result *Result) { result.Series[0].StartedAt = time.Unix(-1, 0) },
		"incarnation newer than generation": func(result *Result) {
			result.GeneratedAt = start.Add(10 * time.Minute)
			result.Series[0].StartedAt = start.Add(10*time.Minute + time.Nanosecond)
			result.Series[0].Points = nil
		},
		"subsecond retention": func(result *Result) { value := start.Add(time.Nanosecond); result.RetainedFrom = &value },
		"duplicate series":    func(result *Result) { result.Series = append(result.Series, result.Series[0]) },
		"out of range":        func(result *Result) { result.Series[0].Points[0].End = now.Add(time.Minute) },
		"subsecond bucket":    func(result *Result) { result.Series[0].Points[0].Start = start.Add(time.Nanosecond) },
		"bucket ends at incarnation": func(result *Result) {
			result.Series[0].StartedAt = result.Series[0].Points[0].End
		},
		"zero coverage value": func(result *Result) { value := 1.0; result.Series[0].Points[0].CPUUtilizationRatio = &value },
		"exclusive end": func(result *Result) {
			point := &result.Series[0].Points[0]
			point.ObservationCount, point.ObservedCount = 1, 1
			point.FirstObservedAt, point.LastObservedAt = point.Start, point.End
			point.CPUContributorCount = 1
			value := 1.0
			point.CPUCapacityCores = &value
		},
		"zero capacity": func(result *Result) {
			point := &result.Series[0].Points[0]
			point.ObservationCount, point.ObservedCount, point.CPUContributorCount = 1, 1, 1
			point.FirstObservedAt, point.LastObservedAt = point.Start, point.Start
			value := 0.0
			point.CPUCapacityCores = &value
		},
		"zero memory limit": func(result *Result) {
			point := &result.Series[0].Points[0]
			point.ObservationCount, point.ObservedCount, point.MemoryContributorCount = 1, 1, 1
			point.FirstObservedAt, point.LastObservedAt = point.Start, point.Start
			value := uint64(0)
			point.MemoryLimitBytes = &value
		},
		"coverage without value": func(result *Result) {
			point := &result.Series[0].Points[0]
			point.ObservationCount, point.ObservedCount, point.CPUContributorCount = 1, 1, 1
			point.FirstObservedAt, point.LastObservedAt = point.Start, point.Start
		},
		"unsafe observation count": func(result *Result) {
			point := &result.Series[0].Points[0]
			point.ObservationCount = int(maxSafeInteger) + 1
			point.UnavailableCount = point.ObservationCount
		},
		"unsafe coverage count": func(result *Result) {
			count := int(maxSafeInteger) + 1
			result.Coverage = []CoveragePoint{{Start: start, End: start.Add(time.Minute), ObservationCount: count, UnavailableCount: count}}
		},
		"unsafe coverage sum": func(result *Result) {
			count := int(maxSafeInteger/2 + 1)
			result.Coverage = []CoveragePoint{
				{Start: start, End: start.Add(time.Minute), FirstObservedAt: start, LastObservedAt: start, ObservationCount: count, UnavailableCount: count},
				{Start: start.Add(time.Minute), End: start.Add(2 * time.Minute), FirstObservedAt: start.Add(time.Minute), LastObservedAt: start.Add(time.Minute), ObservationCount: count, UnavailableCount: count},
			}
		},
		"coverage predates retention": func(result *Result) {
			retained := start.Add(30 * time.Second)
			result.RetainedFrom = &retained
			result.Coverage = []CoveragePoint{{
				Start: start, End: start.Add(time.Minute), FirstObservedAt: start.Add(10 * time.Second), LastObservedAt: start.Add(10 * time.Second),
				ObservationCount: 1, UnavailableCount: 1,
			}}
		},
		"series predates retention": func(result *Result) {
			retained := start.Add(30 * time.Second)
			result.RetainedFrom = &retained
			point := &result.Series[0].Points[0]
			point.FirstObservedAt, point.LastObservedAt = start.Add(10*time.Second), start.Add(10*time.Second)
			point.ObservationCount, point.UnavailableCount = 1, 1
		},
		"observation newer than generation": func(result *Result) {
			point := &result.Series[0].Points[0]
			point.ObservationCount, point.ObservedCount = 1, 1
			point.FirstObservedAt, point.LastObservedAt = point.Start, point.Start.Add(time.Second)
			result.GeneratedAt = point.Start
		},
		"token sample outside bucket": func(result *Result) {
			result.TokenUsage = []TokenUsagePoint{{
				Start: start, End: start.Add(time.Minute), SampledAt: start.Add(time.Minute), InputTokens: 1, OutputTokens: 1,
			}}
		},
		"unsafe token count": func(result *Result) {
			result.TokenUsage = []TokenUsagePoint{{
				Start: start, End: start.Add(time.Minute), SampledAt: start, InputTokens: maxSafeInteger + 1,
			}}
		},
	} {
		t.Run(name, func(t *testing.T) {
			result := Result{GeneratedAt: now, Series: []Series{base}}
			result.Series[0].Points = []Point{{Start: start, End: start.Add(time.Minute)}}
			mutate(&result)
			reader := &fakeReader{capabilities: capabilities(), result: result}
			service, err := NewService(fixedScopeResolver{scope: scope}, reader)
			if err != nil {
				t.Fatal(err)
			}
			service.now = func() time.Time { return now }
			if _, err := service.QuerySession(t.Context(), tenantID, sessionID, Range{Start: start, End: now, MaxPoints: 60}); err == nil {
				t.Fatal("malformed backend result accepted")
			}
		})
	}
}

func TestServiceRejectsUnsafeCapabilities(t *testing.T) {
	base := capabilities()
	for _, mutate := range []func(*Capabilities){
		func(value *Capabilities) { value.CollectionMode = CollectionOnRead },
		func(value *Capabilities) { value.MaximumRange = value.Retention + time.Second },
		func(value *Capabilities) { value.Metrics = []Metric{MetricCPU, MetricCPU} },
	} {
		value := base
		value.Metrics = append([]Metric(nil), base.Metrics...)
		mutate(&value)
		if _, err := NewService(fixedScopeResolver{}, &fakeReader{capabilities: value}); err == nil {
			t.Fatalf("unsafe capabilities accepted: %+v", value)
		}
	}
}

func TestResolutionUsesOverflowSafeCeiling(t *testing.T) {
	duration := (time.Duration(1<<63-1) / time.Second) * time.Second
	value := resolution(duration, 2, time.Second)
	if value <= 0 || value < duration/2 || value%time.Second != 0 {
		t.Fatalf("unsafe resolution for large duration: %v", value)
	}
}
