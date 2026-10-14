package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimehistory"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/google/uuid"
)

type runtimeHistoryFixture struct {
	capabilities runtimehistory.Capabilities
	// respond builds the response for the range the handler chose.
	respond   func(runtimehistory.Range) runtimehistory.Response
	err       error
	tenant    string
	session   string
	requested runtimehistory.Range
	calls     int
}

func (f *runtimeHistoryFixture) Capabilities() runtimehistory.Capabilities { return f.capabilities }

func (f *runtimeHistoryFixture) QuerySession(_ context.Context, tenant, session string, requested runtimehistory.Range) (runtimehistory.Response, error) {
	f.calls++
	f.tenant, f.session, f.requested = tenant, session, requested
	if f.respond == nil {
		return runtimehistory.Response{}, f.err
	}
	return f.respond(requested), f.err
}

// historyWith answers Runtime history from service.
func historyWith(service *runtimeHistoryFixture) func(*Dependencies, *testFakes) {
	return func(_ *Dependencies, f *testFakes) {
		f.runtimeHistory.capabilities, f.runtimeHistory.querySession = service.Capabilities, service.QuerySession
	}
}

func historyCapabilities() runtimehistory.Capabilities {
	return runtimehistory.Capabilities{
		SampleInterval: 30 * time.Second, Retention: 7 * 24 * time.Hour, MinimumStep: 30 * time.Second,
		MaximumRange: 24 * time.Hour, MaximumPoints: 1_000, MaximumSeries: 64, MaximumTotalPoints: 10_000,
		Metrics: []runtimehistory.Metric{runtimehistory.MetricCPU, runtimehistory.MetricMemory, runtimehistory.MetricTokens},
	}
}

func TestRuntimeHistoryRouteBindsAuthenticatedSessionAndPreservesCoverage(t *testing.T) {
	sessionID, environmentID, allocationID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	scope := runtimehistory.Scope{SessionID: sessionID, EnvironmentID: environmentID}
	zeroRatio := float64(0)
	capacity := 2.0
	zeroMemory := uint64(0)
	limit := uint64(2048)
	service := &runtimeHistoryFixture{capabilities: historyCapabilities()}
	handler, _, tenant := adminTestHandler(t, historyWith(service))
	scope.TenantID = tenant
	service.respond = func(requested runtimehistory.Range) runtimehistory.Response {
		start := requested.Start
		coveragePoint := runtimehistory.CoveragePoint{
			Start: start, End: start.Add(30 * time.Second), FirstObservedAt: start.Add(10 * time.Second), LastObservedAt: start.Add(10 * time.Second),
			ObservationCount: 1, ObservedCount: 1,
		}
		resourcePoint := runtimehistory.Point{
			Start: start, End: start.Add(30 * time.Second), FirstObservedAt: start.Add(10 * time.Second), LastObservedAt: start.Add(10 * time.Second),
			ObservationCount: 1, ObservedCount: 1, CPUContributorCount: 1, MemoryContributorCount: 1,
			CPUUtilizationRatio: &zeroRatio, CPUCapacityCores: &capacity, MemoryUsageBytes: &zeroMemory, MemoryLimitBytes: &limit,
		}
		return runtimehistory.Response{
			Capabilities: service.capabilities, Scope: scope,
			Requested: requested, Resolution: 30 * time.Second, GeneratedAt: requested.End, RetainedFrom: &start,
			Coverage:   []runtimehistory.CoveragePoint{coveragePoint},
			Series:     []runtimehistory.Series{{Scope: scope, AllocationID: allocationID, StartedAt: start.Add(-time.Minute), ProviderType: "docker", Points: []runtimehistory.Point{resourcePoint}}},
			TokenUsage: []runtimehistory.TokenUsagePoint{{Start: start, End: start.Add(30 * time.Second), SampledAt: start.Add(10 * time.Second), InputTokens: 120, OutputTokens: 30}},
		}
	}
	response := runtimeObservationRequest(handler, adminSessionsPath+sessionID+"/runtime-history?range=1h")
	if response.Code != http.StatusOK {
		t.Fatalf("history returned %d: %s", response.Code, response.Body)
	}
	var value v1.RuntimeHistory
	if json.Unmarshal(response.Body.Bytes(), &value) != nil || value.Object != "agent.runtime_history" || value.Source != "durable" || value.SessionID != sessionID || value.ResolutionSeconds != 30 || value.RequestedRange.End-value.RequestedRange.Start != 3600 || value.Coverage.SampleCount != 1 || value.Coverage.ExpectedSampleCount != 120 || len(value.Coverage.Buckets) != 1 || len(value.Series) != 1 || len(value.Series[0].Points) != 1 || len(value.TokenUsage) != 1 || value.TokenUsage[0].InputTokens != 120 || value.TokenUsage[0].OutputTokens != 30 {
		t.Fatalf("invalid history response: %s", response.Body)
	}
	point := value.Series[0].Points[0]
	if point.CPU == nil || point.CPU.UtilizationRatio == nil || *point.CPU.UtilizationRatio != 0 || point.Memory == nil || point.Memory.UsageBytes == nil || *point.Memory.UsageBytes != 0 {
		t.Fatalf("observed zero was lost: %+v", point)
	}
	if service.calls != 1 || service.tenant != tenant || service.session != sessionID {
		t.Fatalf("history authority mismatch: %+v", service)
	}
}

// The request carries no time, so a caller's clock minutes ahead of or behind
// Core's cannot reject or shift the read: Core's clock ends the range.
func TestRuntimeHistoryRangeEndsOnCoreClock(t *testing.T) {
	service := &runtimeHistoryFixture{capabilities: historyCapabilities(), err: runtimehistory.ErrUnavailable}
	handler, _, _ := adminTestHandler(t, historyWith(service))
	before := time.Now()
	runtimeObservationRequest(handler, adminSessionsPath+uuid.NewString()+"/runtime-history?range=6h")
	after := time.Now()
	step := 3 * time.Minute // 6 hours in 120 buckets
	got := service.requested
	if service.calls != 1 || got.End.Unix()%int64(step/time.Second) != 0 || !got.End.After(before.Add(-step)) || got.End.After(after) ||
		!got.Start.Equal(got.End.Add(-6*time.Hour)) || got.MaxPoints != 120 {
		t.Fatalf("range %v-%v (%d points) does not end at Core's last complete bucket between %v and %v", got.Start, got.End, got.MaxPoints, before, after)
	}
}

func TestRuntimeHistoryRejectsUnsafeQueriesAndFailures(t *testing.T) {
	service := &runtimeHistoryFixture{capabilities: historyCapabilities()}
	handler, _, _ := adminTestHandler(t, historyWith(service))
	sessionID := uuid.NewString()
	for _, query := range []string{
		"?range=", "?range=2h", "?range=7d", "?range=1h&range=1h", "?range=1h&provider=docker", "?range=1h&max_points=60", "?start=1&end=2",
	} {
		response := runtimeObservationRequest(handler, adminSessionsPath+sessionID+"/runtime-history"+query)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("unsafe query %q returned %d: %s", query, response.Code, response.Body)
		}
	}
	if service.calls != 0 {
		t.Fatalf("unsafe query reached history service %d times", service.calls)
	}

	path := adminSessionsPath + sessionID + "/runtime-history?range=1h"
	for _, tc := range []struct {
		err  error
		code int
	}{
		{runtimehistory.ErrInvalidRange, http.StatusInternalServerError},
		{runtimehistory.ErrUnsupported, http.StatusConflict},
		{runtimehistory.ErrUnavailable, http.StatusServiceUnavailable},
		{runtimehistory.ErrInvalidResult, http.StatusServiceUnavailable},
		{sessions.ErrNotFound, http.StatusNotFound},
	} {
		service.err = tc.err
		response := runtimeObservationRequest(handler, path)
		if response.Code != tc.code {
			t.Fatalf("history error %v returned %d: %s", tc.err, response.Code, response.Body)
		}
	}
	service.err = nil
	response := runtimeObservationRequest(handler, path)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("malformed history response returned %d: %s", response.Code, response.Body)
	}
}

func TestRuntimeHistoryRejectsMismatchedServiceResponses(t *testing.T) {
	sessionID := uuid.NewString()
	service := &runtimeHistoryFixture{capabilities: historyCapabilities()}
	handler, _, tenant := adminTestHandler(t, historyWith(service))
	path := adminSessionsPath + sessionID + "/runtime-history"

	for name, mutate := range map[string]func(*runtimehistory.Response){
		"tenant":  func(value *runtimehistory.Response) { value.TenantID = uuid.NewString() },
		"Session": func(value *runtimehistory.Response) { value.SessionID = uuid.NewString() },
		"range": func(value *runtimehistory.Response) {
			value.Requested.Start = value.Requested.Start.Add(30 * time.Second)
		},
	} {
		t.Run(name, func(t *testing.T) {
			service.respond = func(requested runtimehistory.Range) runtimehistory.Response {
				value := runtimehistory.Response{
					Capabilities: service.capabilities,
					Scope: runtimehistory.Scope{
						TenantID: tenant, SessionID: sessionID, EnvironmentID: uuid.NewString(),
					},
					Requested: requested, Resolution: 30 * time.Second, GeneratedAt: requested.End,
				}
				mutate(&value)
				return value
			}
			response := runtimeObservationRequest(handler, path)
			if response.Code != http.StatusServiceUnavailable {
				t.Fatalf("mismatched %s response returned %d: %s", name, response.Code, response.Body)
			}
		})
	}
}

func TestRuntimeHistoryDefaultPointBudgetRespectsCapabilities(t *testing.T) {
	capabilities := historyCapabilities()
	capabilities.MaximumPoints = 60
	service := &runtimeHistoryFixture{capabilities: capabilities, err: runtimehistory.ErrUnavailable}
	handler, _, _ := adminTestHandler(t, historyWith(service))
	response := runtimeObservationRequest(handler, adminSessionsPath+uuid.NewString()+"/runtime-history")
	if response.Code != http.StatusServiceUnavailable || service.calls != 1 || service.requested.MaxPoints != 60 {
		t.Fatalf("default point budget ignored capabilities: status=%d calls=%d requested=%+v", response.Code, service.calls, service.requested)
	}
}

func TestRuntimeHistoryExpectedCoverageUsesOverflowSafeCeiling(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	start := now.Add(-time.Hour)
	huge := (time.Duration(1<<63-1) / time.Second) * time.Second
	capabilities := historyCapabilities()
	capabilities.SampleInterval = huge
	capabilities.Retention = huge
	capabilities.MaximumRange = time.Hour
	value := runtimehistory.Response{
		Capabilities: capabilities,
		Scope: runtimehistory.Scope{
			TenantID: uuid.NewString(), SessionID: uuid.NewString(), EnvironmentID: uuid.NewString(),
		},
		Requested:   runtimehistory.Range{Start: start, End: now, MaxPoints: 60},
		Resolution:  time.Minute,
		GeneratedAt: now,
	}
	response, err := runtimeHistoryResponse(value, value.TenantID, value.SessionID, value.Requested)
	if err != nil || response.Coverage.ExpectedSampleCount != 1 {
		t.Fatalf("unsafe expected sample ceiling: count=%d err=%v", response.Coverage.ExpectedSampleCount, err)
	}
}
