package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/runtimehistory"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

type runtimeHistoryFixture struct {
	capabilities runtimehistory.Capabilities
	response     runtimehistory.Response
	err          error
	tenant       string
	session      string
	requested    runtimehistory.Range
	calls        int
}

func (f *runtimeHistoryFixture) Capabilities() runtimehistory.Capabilities { return f.capabilities }

func (f *runtimeHistoryFixture) QuerySession(_ context.Context, tenant, session string, requested runtimehistory.Range) (runtimehistory.Response, error) {
	f.calls++
	f.tenant, f.session, f.requested = tenant, session, requested
	return f.response, f.err
}

func historyCapabilities(mode runtimehistory.CollectionMode) runtimehistory.Capabilities {
	value := runtimehistory.Capabilities{
		CollectionMode: mode, Retention: 7 * 24 * time.Hour, MinimumStep: 30 * time.Second,
		MaximumRange: 24 * time.Hour, MaximumPoints: 1_000, MaximumSeries: 64, MaximumTotalPoints: 10_000,
		Metrics: []runtimehistory.Metric{runtimehistory.MetricCPU, runtimehistory.MetricMemory, runtimehistory.MetricTokens},
	}
	if mode == runtimehistory.CollectionPeriodic {
		value.SampleInterval = 30 * time.Second
	}
	return value
}

func TestRuntimeHistoryCapabilitiesAreSafeAndDisabledByDefault(t *testing.T) {
	handler, _, _ := testHandler(t)
	response := runtimeObservationRequest(handler, "/v1/agents/runtime-history/capabilities")
	if response.Code != http.StatusOK {
		t.Fatalf("capabilities returned %d: %s", response.Code, response.Body)
	}
	var value v1.RuntimeHistoryCapabilities
	if json.Unmarshal(response.Body.Bytes(), &value) != nil || value.Object != "agent.runtime_history_capabilities" || value.Available || value.Reason == nil || *value.Reason != "not_configured" || value.CollectionMode != nil || value.SampleIntervalSeconds != nil || value.RetentionSeconds != nil || value.MaximumPoints != nil || value.Metrics == nil || len(value.Metrics) != 0 {
		t.Fatalf("unsafe disabled capabilities: %s", response.Body)
	}
	invalid := runtimeObservationRequest(handler, "/v1/agents/runtime-history/capabilities?backend=clickhouse")
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("capability query was accepted: %d %s", invalid.Code, invalid.Body)
	}
}

func TestRuntimeHistoryRequiresQualifiedPeriodicCollection(t *testing.T) {
	service := &runtimeHistoryFixture{capabilities: historyCapabilities(runtimehistory.CollectionOnRead)}
	handler, _, _ := testHandler(t, WithRuntimeHistory(service))
	capabilityResponse := runtimeObservationRequest(handler, "/v1/agents/runtime-history/capabilities")
	var capabilities v1.RuntimeHistoryCapabilities
	if capabilityResponse.Code != http.StatusOK || json.Unmarshal(capabilityResponse.Body.Bytes(), &capabilities) != nil || capabilities.Available || capabilities.Reason == nil || *capabilities.Reason != "periodic_collection_required" || capabilities.CollectionMode == nil || *capabilities.CollectionMode != "on_read" || capabilities.SampleIntervalSeconds != nil {
		t.Fatalf("on-read capability was advertised as durable: %d %s", capabilityResponse.Code, capabilityResponse.Body)
	}
	response := runtimeObservationRequest(handler, "/v1/agents/sessions/"+uuid.NewString()+"/runtime-history?start=1&end=2")
	if response.Code != http.StatusServiceUnavailable || service.calls != 0 {
		t.Fatalf("on-read history reached query service: %d calls=%d body=%s", response.Code, service.calls, response.Body)
	}
}

func TestRuntimeHistoryFailsClosedForMalformedCapabilities(t *testing.T) {
	service := &runtimeHistoryFixture{capabilities: historyCapabilities(runtimehistory.CollectionPeriodic)}
	service.capabilities.Retention = 0
	handler, _, _ := testHandler(t, WithRuntimeHistory(service))
	capabilityResponse := runtimeObservationRequest(handler, "/v1/agents/runtime-history/capabilities")
	var capabilities v1.RuntimeHistoryCapabilities
	if capabilityResponse.Code != http.StatusOK || json.Unmarshal(capabilityResponse.Body.Bytes(), &capabilities) != nil || capabilities.Available || capabilities.Reason == nil || *capabilities.Reason != "not_configured" || capabilities.CollectionMode != nil || len(capabilities.Metrics) != 0 {
		t.Fatalf("malformed capabilities did not fail closed: %d %s", capabilityResponse.Code, capabilityResponse.Body)
	}
	response := runtimeObservationRequest(handler, "/v1/agents/sessions/"+uuid.NewString()+"/runtime-history?start=1&end=2")
	if response.Code != http.StatusServiceUnavailable || service.calls != 0 {
		t.Fatalf("malformed capabilities reached query service: %d calls=%d body=%s", response.Code, service.calls, response.Body)
	}
}

func TestRuntimeHistoryRouteBindsAuthenticatedSessionAndPreservesCoverage(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	start := now.Add(-time.Hour)
	sessionID, environmentID, allocationID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	scope := runtimehistory.Scope{SessionID: sessionID, EnvironmentID: environmentID}
	zeroRatio := float64(0)
	capacity := 2.0
	zeroMemory := uint64(0)
	limit := uint64(2048)
	coveragePoint := runtimehistory.CoveragePoint{
		Start: start, End: start.Add(time.Minute), FirstObservedAt: start.Add(10 * time.Second), LastObservedAt: start.Add(10 * time.Second),
		ObservationCount: 1, ObservedCount: 1,
	}
	resourcePoint := runtimehistory.Point{
		Start: start, End: start.Add(time.Minute), FirstObservedAt: start.Add(10 * time.Second), LastObservedAt: start.Add(10 * time.Second),
		ObservationCount: 1, ObservedCount: 1, CPUContributorCount: 1, MemoryContributorCount: 1,
		CPUUtilizationRatio: &zeroRatio, CPUCapacityCores: &capacity, MemoryUsageBytes: &zeroMemory, MemoryLimitBytes: &limit,
	}
	service := &runtimeHistoryFixture{capabilities: historyCapabilities(runtimehistory.CollectionPeriodic)}
	handler, _, tenant := testHandler(t, WithRuntimeHistory(service))
	scope.TenantID = tenant
	service.response = runtimehistory.Response{
		Capabilities: service.capabilities, Scope: scope,
		Requested: runtimehistory.Range{Start: start, End: now, MaxPoints: 60}, Resolution: time.Minute, GeneratedAt: now, RetainedFrom: &start,
		Coverage:   []runtimehistory.CoveragePoint{coveragePoint},
		Series:     []runtimehistory.Series{{Scope: scope, AllocationID: allocationID, StartedAt: start.Add(-time.Minute), ProviderType: "docker", Points: []runtimehistory.Point{resourcePoint}}},
		TokenUsage: []runtimehistory.TokenUsagePoint{{Start: start, End: start.Add(time.Minute), SampledAt: start.Add(10 * time.Second), InputTokens: 120, OutputTokens: 30}},
	}
	response := runtimeObservationRequest(handler, "/v1/agents/sessions/"+sessionID+"/runtime-history?start="+timeString(start)+"&end="+timeString(now)+"&max_points=60")
	if response.Code != http.StatusOK {
		t.Fatalf("history returned %d: %s", response.Code, response.Body)
	}
	var value v1.RuntimeHistory
	if json.Unmarshal(response.Body.Bytes(), &value) != nil || value.Object != "agent.runtime_history" || value.Source != "durable" || value.SessionID != sessionID || value.ResolutionSeconds != 60 || value.Coverage.SampleCount != 1 || value.Coverage.ExpectedSampleCount != 120 || len(value.Coverage.Buckets) != 1 || len(value.Series) != 1 || len(value.Series[0].Points) != 1 || len(value.TokenUsage) != 1 || value.TokenUsage[0].InputTokens != 120 || value.TokenUsage[0].OutputTokens != 30 {
		t.Fatalf("invalid history response: %s", response.Body)
	}
	point := value.Series[0].Points[0]
	if point.CPU == nil || point.CPU.UtilizationRatio == nil || *point.CPU.UtilizationRatio != 0 || point.Memory == nil || point.Memory.UsageBytes == nil || *point.Memory.UsageBytes != 0 {
		t.Fatalf("observed zero was lost: %+v", point)
	}
	if service.calls != 1 || service.tenant != tenant || service.session != sessionID || !service.requested.Start.Equal(start) || !service.requested.End.Equal(now) || service.requested.MaxPoints != 60 {
		t.Fatalf("history authority/range mismatch: %+v", service)
	}
}

func TestRuntimeHistoryRejectsUnsafeQueriesAndFailures(t *testing.T) {
	service := &runtimeHistoryFixture{capabilities: historyCapabilities(runtimehistory.CollectionPeriodic)}
	handler, _, _ := testHandler(t, WithRuntimeHistory(service))
	sessionID := uuid.NewString()
	for _, query := range []string{
		"", "?start=1", "?start=2&end=1", "?start=x&end=2", "?start=1&end=2&provider=docker", "?start=1&start=1&end=2", "?start=1&end=2&max_points=x", "?start=1&end=2&max_points=1", "?start=1&end=2&max_points=1001", "?start=1&end=90002", "?start=1&end=9223372036854775807",
	} {
		response := runtimeObservationRequest(handler, "/v1/agents/sessions/"+sessionID+"/runtime-history"+query)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("unsafe query %q returned %d: %s", query, response.Code, response.Body)
		}
	}
	if service.calls != 0 {
		t.Fatalf("unsafe query reached history service %d times", service.calls)
	}

	now := time.Now().UTC().Truncate(time.Second)
	path := "/v1/agents/sessions/" + sessionID + "/runtime-history?start=" + timeString(now.Add(-time.Hour)) + "&end=" + timeString(now)
	for _, tc := range []struct {
		err  error
		code int
	}{
		{runtimehistory.ErrInvalidRange, http.StatusBadRequest},
		{runtimehistory.ErrUnsupported, http.StatusConflict},
		{runtimehistory.ErrUnavailable, http.StatusServiceUnavailable},
		{runtimehistory.ErrInvalidResult, http.StatusServiceUnavailable},
		{store.ErrNotFound, http.StatusNotFound},
	} {
		service.err = tc.err
		response := runtimeObservationRequest(handler, path)
		if response.Code != tc.code {
			t.Fatalf("history error %v returned %d: %s", tc.err, response.Code, response.Body)
		}
	}
	service.err = nil
	service.response = runtimehistory.Response{}
	response := runtimeObservationRequest(handler, path)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("malformed history response returned %d: %s", response.Code, response.Body)
	}
}

func TestRuntimeHistoryRejectsMismatchedServiceResponses(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	start := now.Add(-time.Hour)
	sessionID := uuid.NewString()
	service := &runtimeHistoryFixture{capabilities: historyCapabilities(runtimehistory.CollectionPeriodic)}
	handler, _, tenant := testHandler(t, WithRuntimeHistory(service))
	base := runtimehistory.Response{
		Capabilities: service.capabilities,
		Scope: runtimehistory.Scope{
			TenantID: tenant, SessionID: sessionID, EnvironmentID: uuid.NewString(),
		},
		Requested:  runtimehistory.Range{Start: start, End: now, MaxPoints: 60},
		Resolution: time.Minute, GeneratedAt: now,
	}
	path := "/v1/agents/sessions/" + sessionID + "/runtime-history?start=" + timeString(start) + "&end=" + timeString(now) + "&max_points=60"

	for name, mutate := range map[string]func(*runtimehistory.Response){
		"tenant":  func(value *runtimehistory.Response) { value.TenantID = uuid.NewString() },
		"Session": func(value *runtimehistory.Response) { value.SessionID = uuid.NewString() },
		"range": func(value *runtimehistory.Response) {
			value.Requested.Start = value.Requested.Start.Add(30 * time.Second)
		},
	} {
		t.Run(name, func(t *testing.T) {
			service.response = base
			mutate(&service.response)
			response := runtimeObservationRequest(handler, path)
			if response.Code != http.StatusServiceUnavailable {
				t.Fatalf("mismatched %s response returned %d: %s", name, response.Code, response.Body)
			}
		})
	}
}

func TestRuntimeHistoryDefaultPointBudgetRespectsCapabilities(t *testing.T) {
	capabilities := historyCapabilities(runtimehistory.CollectionPeriodic)
	capabilities.MaximumPoints = 60
	service := &runtimeHistoryFixture{capabilities: capabilities, err: runtimehistory.ErrUnavailable}
	handler, _, _ := testHandler(t, WithRuntimeHistory(service))
	now := time.Now().UTC().Truncate(time.Second)
	response := runtimeObservationRequest(handler, "/v1/agents/sessions/"+uuid.NewString()+"/runtime-history?start="+timeString(now.Add(-time.Hour))+"&end="+timeString(now))
	if response.Code != http.StatusServiceUnavailable || service.calls != 1 || service.requested.MaxPoints != 60 {
		t.Fatalf("default point budget ignored capabilities: status=%d calls=%d requested=%+v", response.Code, service.calls, service.requested)
	}
}

func TestRuntimeHistoryExpectedCoverageUsesOverflowSafeCeiling(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	start := now.Add(-time.Hour)
	huge := (time.Duration(1<<63-1) / time.Second) * time.Second
	capabilities := historyCapabilities(runtimehistory.CollectionPeriodic)
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

func timeString(value time.Time) string { return fmt.Sprintf("%d", value.Unix()) }
