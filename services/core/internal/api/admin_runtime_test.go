package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimeobs"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
	"github.com/google/uuid"
)

type adminRuntimeTargets struct {
	page      store.AdminRuntimeTargetPage
	tenants   []string
	after     string
	limit     int
	ascending bool
}

func (s *adminRuntimeTargets) ListAdminRuntimeTargets(_ context.Context, tenants []string, after string, limit int, ascending bool) (store.AdminRuntimeTargetPage, error) {
	s.tenants, s.after, s.limit, s.ascending = tenants, after, limit, ascending
	return s.page, nil
}

const adminRuntimeObservationsPath = "/core/v1/sandbox/runtime-observations"

// adminRuntimeFixture serves the administrator observation list over one
// Project per tenant and the given Session targets.
func adminRuntimeFixture(t *testing.T, projects []store.Project, targets []store.AdminRuntimeTarget, service RuntimeObservations) (http.Handler, *adminRuntimeTargets) {
	t.Helper()
	deps, fakes := managementFakes(t, callerBinding())
	fakes.projects.listProjects = func(context.Context, string, int, bool) (store.ProjectPage, error) {
		return store.ProjectPage{Data: projects}, nil
	}
	management := &adminRuntimeTargets{page: store.AdminRuntimeTargetPage{Data: targets, HasMore: true}}
	fakes.admin.listAdminRuntimeTargets = management.ListAdminRuntimeTargets
	observeWith(service)(&deps, fakes)
	return newTestHandler(t, deps), management
}

// HEAD never samples Runtime or queries history on the administrator routes.
func TestAdminRuntimeRoutesRejectHead(t *testing.T) {
	calls := 0
	service := runtimeObservationServiceFunc(func(context.Context, string, string) (runtimeobs.Observation, error) {
		calls++
		return runtimeobs.Observation{}, errors.New("sampled")
	})
	handler, _ := adminRuntimeFixture(t, nil, nil, service)
	for _, path := range []string{adminRuntimeObservationsPath, adminSessionsPath + uuid.NewString() + "/runtime-observation", adminSessionsPath + uuid.NewString() + "/runtime-history?start=1&end=2"} {
		response := serve(handler, http.MethodHead, path, "", withHeaders([]string{"Authorization", "Bearer admin"}))
		if response.Code != http.StatusMethodNotAllowed || response.Header().Get("Allow") != "GET" || response.Header().Get("Content-Type") != "application/json" {
			t.Errorf("HEAD %s = %d %q", path, response.Code, response.Header().Get("Allow"))
		}
	}
	if calls != 0 {
		t.Fatal("HEAD sampled a Runtime")
	}
}

func unsupportedObservation(session string, at time.Time) runtimeobs.Observation {
	return runtimeobs.Observation{
		Target: runtimeobs.Target{SessionID: session, Mode: runtimeobs.ModeNone},
		Status: runtimeobs.StatusUnsupported, Reason: "runtime_mode_not_observable", ResolvedAt: at,
	}
}

func TestAdminRuntimeObservationListKeepsTargetOrderAndProjects(t *testing.T) {
	now := time.Date(2026, 9, 22, 8, 0, 0, 0, time.UTC)
	projects := []store.Project{{ID: uuid.NewString(), TenantID: uuid.NewString()}, {ID: uuid.NewString(), TenantID: uuid.NewString()}}
	var targets []store.AdminRuntimeTarget
	for index := range 3 {
		targets = append(targets, store.AdminRuntimeTarget{SessionID: uuid.NewString(), TenantID: projects[index%2].TenantID})
	}
	service := runtimeObservationServiceFunc(func(_ context.Context, tenant, session string) (runtimeobs.Observation, error) {
		if !slices.Contains(targets, store.AdminRuntimeTarget{SessionID: session, TenantID: tenant}) {
			return runtimeobs.Observation{}, errors.New("unexpected tenant")
		}
		return unsupportedObservation(session, now), nil
	})
	handler, management := adminRuntimeFixture(t, projects, targets, service)

	response := runtimeObservationRequest(handler, adminRuntimeObservationsPath+"?after=cursor&limit=3&order=asc")
	var page AdminRuntimeObservationList
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &page) != nil {
		t.Fatalf("list returned %d: %s", response.Code, response.Body)
	}
	if !slices.Equal(management.tenants, []string{projects[0].TenantID, projects[1].TenantID}) || management.after != "cursor" || management.limit != 3 || !management.ascending {
		t.Fatalf("pagination binding was not preserved: %+v", management)
	}
	if len(page.Data) != len(targets) || !page.HasMore || page.FirstID == nil || *page.FirstID != targets[0].SessionID || page.LastID == nil || *page.LastID != targets[2].SessionID {
		t.Fatalf("invalid page: %s", response.Body)
	}
	for index, item := range page.Data {
		if item.Observation.ID != targets[index].SessionID || item.ProjectID != projects[index%2].ID {
			t.Fatalf("concurrent collection reordered or mislabelled the page: %s", response.Body)
		}
	}
}

// The page is read in one batch with the shared concurrency and per-source bounds.
func TestAdminRuntimeObservationListBoundsCollection(t *testing.T) {
	now := time.Date(2026, 9, 22, 8, 0, 0, 0, time.UTC)
	project := store.Project{ID: uuid.NewString(), TenantID: uuid.NewString()}
	var targets []store.AdminRuntimeTarget
	for range 20 {
		targets = append(targets, store.AdminRuntimeTarget{SessionID: uuid.NewString(), TenantID: project.TenantID})
	}
	var options runtimeobs.PageOptions
	service := runtimeObservationPageRecorder{options: &options, runtimeObservationServiceFunc: func(_ context.Context, _, session string) (runtimeobs.Observation, error) {
		return unsupportedObservation(session, now), nil
	}}
	handler, _ := adminRuntimeFixture(t, []store.Project{project}, targets, service)
	if response := runtimeObservationRequest(handler, adminRuntimeObservationsPath+"?limit=20"); response.Code != http.StatusOK {
		t.Fatalf("list returned %d: %s", response.Code, response.Body)
	}
	if options.Concurrency != runtimeObservationConcurrency || options.SourceTimeout != runtimeObservationSourceBudget {
		t.Fatalf("collection bounds = %+v", options)
	}
}

func TestAdminRuntimeObservationListRejectsWholePageOnIntegrityFailure(t *testing.T) {
	now := time.Date(2026, 9, 22, 8, 0, 0, 0, time.UTC)
	project := store.Project{ID: uuid.NewString(), TenantID: uuid.NewString()}
	valid, invalid := uuid.NewString(), uuid.NewString()
	service := runtimeObservationServiceFunc(func(_ context.Context, _, session string) (runtimeobs.Observation, error) {
		if session == invalid {
			return runtimeobs.Observation{Target: runtimeobs.Target{Mode: runtimeobs.ModeNone}, Status: runtimeobs.StatusUnsupported, ResolvedAt: now}, nil
		}
		return unsupportedObservation(session, now), nil
	})
	handler, _ := adminRuntimeFixture(t, []store.Project{project}, []store.AdminRuntimeTarget{{SessionID: valid, TenantID: project.TenantID}, {SessionID: invalid, TenantID: project.TenantID}}, service)

	response := runtimeObservationRequest(handler, adminRuntimeObservationsPath+"?limit=2")
	var envelope struct {
		Data  json.RawMessage            `json:"data"`
		Error map[string]json.RawMessage `json:"error"`
	}
	if response.Code != http.StatusInternalServerError || json.Unmarshal(response.Body.Bytes(), &envelope) != nil || len(envelope.Error) == 0 || envelope.Data != nil {
		t.Fatalf("integrity failure returned %d or leaked a partial page: %s", response.Code, response.Body)
	}
}
