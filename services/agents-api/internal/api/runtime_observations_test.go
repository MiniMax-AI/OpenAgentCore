package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/runtimeobs"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

type runtimeObservationFixture struct {
	values map[string]runtimeobs.Observation
}

func (f runtimeObservationFixture) ObserveSession(_ context.Context, tenant, session string) (runtimeobs.Observation, error) {
	value := f.values[session]
	value.Target.TenantID = tenant
	return value, nil
}

func (f runtimeObservationFixture) ObserveSessions(ctx context.Context, sessions []runtimeobs.SessionIdentity, _ runtimeobs.PageOptions) ([]runtimeobs.Observation, []error) {
	return observeEach(ctx, sessions, f.ObserveSession)
}

type runtimeObservationServiceFunc func(context.Context, string, string) (runtimeobs.Observation, error)

func (f runtimeObservationServiceFunc) ObserveSession(ctx context.Context, tenant, session string) (runtimeobs.Observation, error) {
	return f(ctx, tenant, session)
}

func (f runtimeObservationServiceFunc) ObserveSessions(ctx context.Context, sessions []runtimeobs.SessionIdentity, _ runtimeobs.PageOptions) ([]runtimeobs.Observation, []error) {
	return observeEach(ctx, sessions, f)
}

func observeEach(ctx context.Context, sessions []runtimeobs.SessionIdentity, observe func(context.Context, string, string) (runtimeobs.Observation, error)) ([]runtimeobs.Observation, []error) {
	observations, errs := make([]runtimeobs.Observation, len(sessions)), make([]error, len(sessions))
	for index, session := range sessions {
		observations[index], errs[index] = observe(ctx, session.TenantID, session.SessionID)
	}
	return observations, errs
}

// runtimeObservationPageRecorder records the page bounds a list route requests.
type runtimeObservationPageRecorder struct {
	runtimeObservationServiceFunc
	options *runtimeobs.PageOptions
}

func (r runtimeObservationPageRecorder) ObserveSessions(ctx context.Context, sessions []runtimeobs.SessionIdentity, options runtimeobs.PageOptions) ([]runtimeobs.Observation, []error) {
	*r.options = options
	return observeEach(ctx, sessions, r.runtimeObservationServiceFunc)
}

func runtimeObservationRequest(handler http.Handler, path string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.Header.Set("Authorization", "Bearer test-api-key")
	request.Header.Set("OpenAI-Beta", "agents=v1")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func TestRuntimeObservationRoutesUseSessionIdentityAndExactNullability(t *testing.T) {
	now := time.Date(2026, 9, 22, 8, 0, 0, 0, time.UTC)
	sessionID := uuid.NewString()
	service := runtimeObservationFixture{values: map[string]runtimeobs.Observation{
		sessionID: {Target: runtimeobs.Target{SessionID: sessionID, Mode: runtimeobs.ModeNone}, Status: runtimeobs.StatusUnsupported, Reason: "runtime_mode_not_observable", ResolvedAt: now},
	}}
	handler, saved, _ := testHandler(t, WithRuntimeObservations(service))
	saved.sessions = []store.Session{{ID: sessionID, CreatedAt: now}}

	for _, path := range []string{"/v1/agents/sessions/" + sessionID + "/runtime-observation", "/v1/agents/runtime-observations?limit=1"} {
		response := runtimeObservationRequest(handler, path)
		if response.Code != http.StatusOK {
			t.Fatalf("%s returned %d: %s", path, response.Code, response.Body)
		}
		if path[len(path)-7:] == "limit=1" {
			var page v1.RuntimeObservationList
			if json.Unmarshal(response.Body.Bytes(), &page) != nil || len(page.Data) != 1 || page.FirstID == nil || *page.FirstID != sessionID || page.LastID == nil || *page.LastID != sessionID {
				t.Fatalf("invalid observation page: %s", response.Body)
			}
			continue
		}
		var value v1.RuntimeObservation
		if json.Unmarshal(response.Body.Bytes(), &value) != nil || value.ID != sessionID || value.SessionID != sessionID || value.Instance.Kind != "none" || value.EnvironmentID != nil || value.ProviderType != nil || value.ObservedAt != nil || value.CPU != nil || value.Memory != nil || value.Reason == nil || *value.Reason != "runtime_mode_not_observable" {
			t.Fatalf("invalid unsupported observation: %s", response.Body)
		}
	}
}

func TestRuntimeObservationRoutesRequireConfiguredServiceAndRejectQueries(t *testing.T) {
	handler, _, _ := testHandler(t)
	missing := runtimeObservationRequest(handler, "/v1/agents/runtime-observations")
	if missing.Code != http.StatusServiceUnavailable {
		t.Fatalf("unconfigured service returned %d: %s", missing.Code, missing.Body)
	}

	sessionID := uuid.NewString()
	now := time.Now().UTC()
	service := runtimeObservationFixture{values: map[string]runtimeobs.Observation{
		sessionID: {Target: runtimeobs.Target{SessionID: sessionID, Mode: runtimeobs.ModeNone}, Status: runtimeobs.StatusUnsupported, Reason: "runtime_mode_not_observable", ResolvedAt: now},
	}}
	handler, _, _ = testHandler(t, WithRuntimeObservations(service))
	invalid := runtimeObservationRequest(handler, "/v1/agents/sessions/"+sessionID+"/runtime-observation?provider=docker")
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("unsupported query returned %d: %s", invalid.Code, invalid.Body)
	}
}

func TestRuntimeObservationResponsePreservesObservedZero(t *testing.T) {
	zeroCPU := float64(0)
	zeroMemory := uint64(0)
	now := time.Date(2026, 9, 22, 8, 0, 0, 0, time.UTC)
	sessionID, environmentID := uuid.NewString(), uuid.NewString()
	value, err := runtimeObservationResponse(runtimeobs.Observation{
		Target: runtimeobs.Target{SessionID: sessionID, EnvironmentID: environmentID, Mode: runtimeobs.ModeManaged, Instance: runtimeobs.Instance{AllocationID: uuid.NewString(), DeviceID: uuid.NewString(), AllocationState: "running", ComputePhase: "running", AllocationCreatedAt: now.Add(-time.Hour)}},
		Status: runtimeobs.StatusObserved, ProviderType: "docker", ResolvedAt: now,
		Sample: &runtimeobs.Sample{ObservedAt: now, CPUUsageSecondsTotal: &zeroCPU, MemoryUsageBytes: &zeroMemory},
	})
	if err != nil || value.LifecycleState == nil || *value.LifecycleState != "active" || value.CPU == nil || value.CPU.UsageSecondsTotal == nil || *value.CPU.UsageSecondsTotal != 0 || value.Memory == nil || value.Memory.UsageBytes == nil || *value.Memory.UsageBytes != 0 {
		t.Fatalf("observed zero was lost: %+v %v", value, err)
	}
}

func TestRuntimeLifecycleStateProjectsProviderNeutralPhases(t *testing.T) {
	for _, item := range []struct {
		state, phase, want string
	}{
		{state: "", phase: "", want: "pending"},
		{state: "creating", phase: "disabled", want: "pending"},
		{state: "running", phase: "disabled", want: "active"},
		{state: "running", phase: "running", want: "active"},
		{state: "running", phase: "quiescing", want: "transitioning"},
		{state: "running", phase: "suspending", want: "transitioning"},
		{state: "running", phase: "suspended", want: "sleeping"},
		{state: "running", phase: "restoring", want: "transitioning"},
		{state: "running", phase: "waking", want: "transitioning"},
		{state: "cleanup_pending", phase: "disabled", want: "stopped"},
		{state: "released", phase: "disabled", want: "stopped"},
	} {
		got, err := runtimeLifecycleState(runtimeobs.Instance{AllocationState: item.state, ComputePhase: item.phase})
		if err != nil || got != item.want {
			t.Fatalf("state=%s phase=%s got=%s want=%s err=%v", item.state, item.phase, got, item.want, err)
		}
	}
}

func TestRuntimeObservationResponseRejectsTimesOutsidePublicContract(t *testing.T) {
	now := time.Date(2026, 9, 22, 8, 0, 0, 0, time.UTC)
	preEpoch := time.Unix(-1, 0).UTC()
	base := runtimeobs.Observation{
		Target: runtimeobs.Target{
			SessionID: uuid.NewString(), EnvironmentID: uuid.NewString(), Mode: runtimeobs.ModeManaged,
			Instance: runtimeobs.Instance{AllocationID: uuid.NewString(), DeviceID: uuid.NewString(), AllocationCreatedAt: now.Add(-time.Hour)},
		},
		Status: runtimeobs.StatusObserved, ResolvedAt: now,
		Sample: &runtimeobs.Sample{ObservedAt: now, StartedAt: timePointer(now.Add(-time.Minute))},
	}
	for _, mutate := range []func(*runtimeobs.Observation){
		func(value *runtimeobs.Observation) { value.Target.Instance.AllocationCreatedAt = now.Add(time.Second) },
		func(value *runtimeobs.Observation) { value.Target.Instance.AllocationCreatedAt = preEpoch },
		func(value *runtimeobs.Observation) { value.Sample.ObservedAt = preEpoch },
		func(value *runtimeobs.Observation) { value.Sample.StartedAt = &preEpoch },
	} {
		observation := base
		sample := *base.Sample
		observation.Sample = &sample
		mutate(&observation)
		if _, err := runtimeObservationResponse(observation); err == nil {
			t.Fatalf("invalid Runtime time accepted: %+v", observation)
		}
	}
}

func timePointer(value time.Time) *time.Time { return &value }

func TestRuntimeObservationListPreservesStoreOrderAndTenantPagination(t *testing.T) {
	now := time.Date(2026, 9, 22, 8, 0, 0, 0, time.UTC)
	sessionIDs := []string{uuid.NewString(), uuid.NewString(), uuid.NewString()}
	var expectedTenant string
	service := runtimeObservationServiceFunc(func(_ context.Context, tenant, session string) (runtimeobs.Observation, error) {
		if tenant != expectedTenant {
			return runtimeobs.Observation{}, errors.New("unexpected tenant")
		}
		if session == sessionIDs[0] {
			time.Sleep(20 * time.Millisecond)
		}
		return runtimeobs.Observation{
			Target: runtimeobs.Target{SessionID: session, Mode: runtimeobs.ModeNone},
			Status: runtimeobs.StatusUnsupported, Reason: "runtime_mode_not_observable", ResolvedAt: now,
		}, nil
	})
	handler, saved, tenant := testHandler(t, WithRuntimeObservations(service))
	expectedTenant = tenant
	for _, id := range sessionIDs {
		saved.sessions = append(saved.sessions, store.Session{ID: id, CreatedAt: now})
	}
	saved.nextSessionCursor = "next"

	response := runtimeObservationRequest(handler, "/v1/agents/runtime-observations?after=cursor&limit=3&order=asc")
	if response.Code != http.StatusOK {
		t.Fatalf("list returned %d: %s", response.Code, response.Body)
	}
	var page v1.RuntimeObservationList
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Data) != len(sessionIDs) || !page.HasMore || saved.listTenant != tenant || saved.listAfter != "cursor" || saved.listLimit != 3 || !saved.listAscending {
		t.Fatalf("pagination binding was not preserved: page=%+v store=%+v", page, saved)
	}
	for index, item := range page.Data {
		if item.ID != sessionIDs[index] {
			t.Fatalf("concurrent collection reordered page: %+v", page.Data)
		}
	}
}

func TestRuntimeObservationListBoundsCollectionConcurrency(t *testing.T) {
	now := time.Date(2026, 9, 22, 8, 0, 0, 0, time.UTC)
	var options runtimeobs.PageOptions
	service := runtimeObservationPageRecorder{options: &options, runtimeObservationServiceFunc: func(_ context.Context, _, session string) (runtimeobs.Observation, error) {
		return runtimeobs.Observation{
			Target: runtimeobs.Target{SessionID: session, Mode: runtimeobs.ModeNone},
			Status: runtimeobs.StatusUnsupported, Reason: "runtime_mode_not_observable", ResolvedAt: now,
		}, nil
	}}
	handler, saved, _ := testHandler(t, WithRuntimeObservations(service))
	for range 20 {
		saved.sessions = append(saved.sessions, store.Session{ID: uuid.NewString(), CreatedAt: now})
	}
	response := runtimeObservationRequest(handler, "/v1/agents/runtime-observations?limit=20")
	if response.Code != http.StatusOK {
		t.Fatalf("list returned %d: %s", response.Code, response.Body)
	}
	if options.Concurrency != runtimeObservationConcurrency || options.SourceTimeout != runtimeObservationSourceBudget {
		t.Fatalf("collection bounds = %+v", options)
	}
}

func TestRuntimeObservationListRejectsWholePageOnIntegrityFailure(t *testing.T) {
	now := time.Date(2026, 9, 22, 8, 0, 0, 0, time.UTC)
	validID, invalidID := uuid.NewString(), uuid.NewString()
	service := runtimeObservationFixture{values: map[string]runtimeobs.Observation{
		validID: {
			Target: runtimeobs.Target{SessionID: validID, Mode: runtimeobs.ModeNone},
			Status: runtimeobs.StatusUnsupported, Reason: "runtime_mode_not_observable", ResolvedAt: now,
		},
		invalidID: {Target: runtimeobs.Target{Mode: runtimeobs.ModeNone}, Status: runtimeobs.StatusUnsupported, ResolvedAt: now},
	}}
	handler, saved, _ := testHandler(t, WithRuntimeObservations(service))
	saved.sessions = []store.Session{{ID: validID, CreatedAt: now}, {ID: invalidID, CreatedAt: now}}

	response := runtimeObservationRequest(handler, "/v1/agents/runtime-observations?limit=2")
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("integrity failure returned %d: %s", response.Code, response.Body)
	}
	var envelope struct {
		Error map[string]json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil || len(envelope.Error) == 0 {
		t.Fatalf("integrity failure leaked a partial page: %s", response.Body)
	}
}

func TestAdminRuntimeObservationProjectsReportedUtilizationAndDisk(t *testing.T) {
	now := time.Date(2026, 9, 25, 10, 31, 37, 0, time.UTC)
	ratio, cores := .1955, 2.0
	memoryUsed, memoryTotal, diskUsed, diskTotal := uint64(183836672), uint64(2079141888), uint64(1593188352), uint64(23511863296)
	observation := runtimeobs.Observation{
		Target: runtimeobs.Target{SessionID: uuid.NewString(), EnvironmentID: uuid.NewString(), Mode: runtimeobs.ModeManaged,
			Instance: runtimeobs.Instance{AllocationID: uuid.NewString(), AllocationState: "running", ComputePhase: "disabled", AllocationCreatedAt: now.Add(-time.Minute)}},
		Status: runtimeobs.StatusObserved, ProviderType: "e2b", ResolvedAt: now,
		Sample: &runtimeobs.Sample{ObservedAt: now, StartedAt: timePointer(now.Add(-time.Minute)), CPUUtilizationRatio: &ratio, CPUCapacityCores: &cores,
			MemoryUsageBytes: &memoryUsed, MemoryLimitBytes: &memoryTotal, DiskUsageBytes: &diskUsed, DiskLimitBytes: &diskTotal},
	}
	value, err := adminRuntimeObservationResponse(observation)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(value)
	for _, want := range []string{
		`"cpu":{"usage_seconds_total":null,"capacity_cores":2,"usage_cores":null,"utilization_ratio":0.1955}`,
		`"memory":{"usage_bytes":183836672,"limit_bytes":2079141888}`,
		`"disk":{"usage_bytes":1593188352,"limit_bytes":23511863296}`,
	} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("E2B observation lacks %s: %s", want, raw)
		}
	}
	public, _ := json.Marshal(value.RuntimeObservation)
	observation.Sample.DiskUsageBytes, observation.Sample.DiskLimitBytes = nil, nil
	value, err = adminRuntimeObservationResponse(observation)
	raw, _ = json.Marshal(value)
	if err != nil || !strings.Contains(string(raw), `"disk":null`) || strings.Contains(string(public), "disk") {
		t.Fatalf("unreported disk was not null or reached the project shape: %s %s %v", raw, public, err)
	}
}
