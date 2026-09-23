package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

// missingResourceStore reports every resource as missing, recording the tenant
// each lookup used.
type missingResourceStore struct {
	ResourceStore
	tenants []string
}

func (s *missingResourceStore) missing(tenant string) error {
	s.tenants = append(s.tenants, tenant)
	return store.ErrNotFound
}

func (s *missingResourceStore) GetAgent(_ context.Context, tenant, _ string) (store.SavedAgent, error) {
	return store.SavedAgent{}, s.missing(tenant)
}

func (s *missingResourceStore) DeleteAgent(_ context.Context, tenant, _ string) (string, error) {
	return "", s.missing(tenant)
}

func (s *missingResourceStore) UpdateAgent(_ context.Context, tenant, _ string, _ store.UpdateAgentInput) (store.SavedAgent, error) {
	return store.SavedAgent{}, s.missing(tenant)
}

func (s *missingResourceStore) GetSession(_ context.Context, tenant, _ string) (store.Session, error) {
	return store.Session{}, s.missing(tenant)
}

func (s *missingResourceStore) DeleteSession(_ context.Context, tenant, _ string) error {
	return s.missing(tenant)
}

func (s *missingResourceStore) UpdateSessionMetadata(_ context.Context, tenant, _ string, _ map[string]string) (store.Session, error) {
	return store.Session{}, s.missing(tenant)
}

func (s *missingResourceStore) GetEnvironmentTemplate(_ context.Context, tenant, _ string) (store.EnvironmentTemplate, error) {
	return store.EnvironmentTemplate{}, s.missing(tenant)
}

func (s *missingResourceStore) UpdateEnvironmentTemplate(_ context.Context, tenant, _ string, _ store.EnvironmentTemplateInput) (store.EnvironmentTemplate, error) {
	return store.EnvironmentTemplate{}, s.missing(tenant)
}

func (s *missingResourceStore) DeleteEnvironmentTemplate(_ context.Context, tenant, _ string) (string, error) {
	return "", s.missing(tenant)
}

// Unknown query keys on single-resource routes are ignored: a missing or foreign
// resource keeps the same not-found response and authenticated tenant.
func TestSingleResourceRoutesIgnoreUnknownQueryKeys(t *testing.T) {
	id := uuid.NewString()
	for _, route := range []struct{ method, path, body string }{
		{http.MethodGet, "/v1/agents/" + id, ""},
		{http.MethodPost, "/v1/agents/" + id, `{"name":"Renamed"}`},
		{http.MethodDelete, "/v1/agents/" + id, ""},
		{http.MethodGet, "/v1/agents/sessions/" + id, ""},
		{http.MethodPost, "/v1/agents/sessions/" + id, `{"metadata":{"key":"value"}}`},
		{http.MethodDelete, "/v1/agents/sessions/" + id, ""},
		{http.MethodGet, "/v1/agents/environments/templates/" + id, ""},
		{http.MethodPost, "/v1/agents/environments/templates/" + id, `{"name":"Renamed"}`},
		{http.MethodDelete, "/v1/agents/environments/templates/" + id, ""},
	} {
		t.Run(route.method+" "+route.path, func(t *testing.T) {
			s := &missingResourceStore{}
			tenant := uuid.NewString()
			auth, err := NewAuthenticator([]APIKey{{OrganizationID: "test-org", ProjectID: uuid.NewString(), SubjectKind: "service_account", SubjectID: "test-runner", TokenSHA256: device.HashCredential("test-api-key"), TenantID: tenant}})
			if err != nil {
				t.Fatal(err)
			}
			h, err := NewHandler(s, auth, "codex")
			if err != nil {
				t.Fatal(err)
			}
			var bodies []string
			for _, query := range []string{"", "?tenant_id=foreign&include=files&unknown=1&unknown=2"} {
				r := httptest.NewRequest(route.method, route.path+query, strings.NewReader(route.body))
				r.Header.Set("Authorization", "Bearer test-api-key")
				r.Header.Set("OpenAI-Beta", "agents=v1")
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				if w.Code != http.StatusNotFound {
					t.Fatalf("%s: %d %s", query, w.Code, w.Body)
				}
				bodies = append(bodies, w.Body.String())
			}
			if bodies[0] != bodies[1] || len(s.tenants) != 2 || s.tenants[0] != tenant || s.tenants[1] != tenant {
				t.Fatalf("query changed the response or tenant: %v %v", bodies, s.tenants)
			}
		})
	}
}

// missingSkillStore reports every Skill as missing and records list parameters.
type missingSkillStore struct {
	SkillStore
	tenants []string
	limit   int
	hasMore bool
}

func (s *missingSkillStore) GetSkill(_ context.Context, tenant, _ string) (store.Skill, error) {
	s.tenants = append(s.tenants, tenant)
	return store.Skill{}, store.ErrNotFound
}

func (s *missingSkillStore) DeleteSkill(_ context.Context, tenant, _ string) error {
	s.tenants = append(s.tenants, tenant)
	return store.ErrNotFound
}

func (s *missingSkillStore) GetSkillVersion(_ context.Context, tenant, _, _ string) (store.SkillVersion, error) {
	s.tenants = append(s.tenants, tenant)
	return store.SkillVersion{}, store.ErrNotFound
}

func (s *missingSkillStore) ListSkills(_ context.Context, tenant, _ string, limit int, _ bool) (store.SkillPage, error) {
	s.tenants, s.limit = append(s.tenants, tenant), limit
	return store.SkillPage{HasMore: s.hasMore}, nil
}

func (s *missingSkillStore) ListSkillVersions(_ context.Context, tenant, _, _ string, limit int, _ bool) (store.SkillVersionPage, error) {
	s.tenants, s.limit = append(s.tenants, tenant), limit
	return store.SkillVersionPage{HasMore: s.hasMore}, nil
}

func skillQueryHandler(t *testing.T, s *missingSkillStore) (http.Handler, string) {
	t.Helper()
	tenant := uuid.NewString()
	auth, err := NewAuthenticator([]APIKey{{OrganizationID: "test-org", ProjectID: uuid.NewString(), SubjectKind: "service_account", SubjectID: "test-runner", TokenSHA256: device.HashCredential("test-api-key"), TenantID: tenant}})
	if err != nil {
		t.Fatal(err)
	}
	h, err := NewHandler(&missingResourceStore{}, auth, "codex", WithSkills(s))
	if err != nil {
		t.Fatal(err)
	}
	return h, tenant
}

func skillQueryRequest(h http.Handler, method, path string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, nil)
	r.Header.Set("Authorization", "Bearer test-api-key")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestSkillResourceRoutesIgnoreUnknownQueryKeys(t *testing.T) {
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/v1/skills/skill_missing"},
		{http.MethodDelete, "/v1/skills/skill_missing"},
		{http.MethodGet, "/v1/skills/skill_missing/versions/1"},
	} {
		t.Run(route.method+" "+route.path, func(t *testing.T) {
			s := &missingSkillStore{}
			h, tenant := skillQueryHandler(t, s)
			plain := skillQueryRequest(h, route.method, route.path)
			query := skillQueryRequest(h, route.method, route.path+"?tenant_id=foreign&limit=5&unknown=1")
			if plain.Code != http.StatusNotFound || query.Code != plain.Code || query.Body.String() != plain.Body.String() || len(s.tenants) != 2 || s.tenants[1] != tenant {
				t.Fatalf("query changed the response: %d %s / %d %s %v", plain.Code, plain.Body, query.Code, query.Body, s.tenants)
			}
		})
	}
}

func TestSkillListLimitZeroReturnsEmptyPage(t *testing.T) {
	for _, path := range []string{"/v1/skills", "/v1/skills/skill_example/versions"} {
		for _, hasMore := range []bool{true, false} {
			s := &missingSkillStore{hasMore: hasMore}
			h, tenant := skillQueryHandler(t, s)
			w := skillQueryRequest(h, http.MethodGet, path+"?limit=0&unknown=1")
			want := fmt.Sprintf(`{"object":"list","data":[],"first_id":null,"last_id":null,"has_more":%t}`, hasMore)
			var got, expected any
			if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &got) != nil || json.Unmarshal([]byte(want), &expected) != nil || !reflect.DeepEqual(got, expected) {
				t.Fatalf("%s: %d %s", path, w.Code, w.Body)
			}
			if s.limit != 0 || len(s.tenants) != 1 || s.tenants[0] != tenant {
				t.Fatalf("zero page parameters: limit=%d tenants=%v", s.limit, s.tenants)
			}
		}
	}
}
