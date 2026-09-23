package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
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

type runtimeObservationServiceFunc func(context.Context, string, string) (runtimeobs.Observation, error)

func (f runtimeObservationServiceFunc) ObserveSession(ctx context.Context, tenant, session string) (runtimeobs.Observation, error) {
	return f(ctx, tenant, session)
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
		Target: runtimeobs.Target{SessionID: sessionID, EnvironmentID: environmentID, Mode: runtimeobs.ModeManaged, Instance: runtimeobs.Instance{AllocationID: uuid.NewString(), DeviceID: uuid.NewString(), AllocationCreatedAt: now.Add(-time.Hour)}},
		Status: runtimeobs.StatusObserved, ProviderType: "docker", ResolvedAt: now,
		Sample: &runtimeobs.Sample{ObservedAt: now, CPUUsageSecondsTotal: &zeroCPU, MemoryUsageBytes: &zeroMemory},
	})
	if err != nil || value.CPU == nil || value.CPU.UsageSecondsTotal == nil || *value.CPU.UsageSecondsTotal != 0 || value.Memory == nil || value.Memory.UsageBytes == nil || *value.Memory.UsageBytes != 0 {
		t.Fatalf("observed zero was lost: %+v %v", value, err)
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
	var active, maximum atomic.Int32
	service := runtimeObservationServiceFunc(func(_ context.Context, _, session string) (runtimeobs.Observation, error) {
		current := active.Add(1)
		defer active.Add(-1)
		for current > maximum.Load() && !maximum.CompareAndSwap(maximum.Load(), current) {
		}
		time.Sleep(15 * time.Millisecond)
		return runtimeobs.Observation{
			Target: runtimeobs.Target{SessionID: session, Mode: runtimeobs.ModeNone},
			Status: runtimeobs.StatusUnsupported, Reason: "runtime_mode_not_observable", ResolvedAt: now,
		}, nil
	})
	handler, saved, _ := testHandler(t, WithRuntimeObservations(service))
	for range 20 {
		saved.sessions = append(saved.sessions, store.Session{ID: uuid.NewString(), CreatedAt: now})
	}
	response := runtimeObservationRequest(handler, "/v1/agents/runtime-observations?limit=20")
	if response.Code != http.StatusOK {
		t.Fatalf("list returned %d: %s", response.Code, response.Body)
	}
	if got := maximum.Load(); got == 0 || got > runtimeObservationConcurrency {
		t.Fatalf("collection concurrency = %d, want 1..%d", got, runtimeObservationConcurrency)
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
