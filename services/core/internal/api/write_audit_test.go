package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/obs/log"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/identity"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/projects"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/writeaudit"
	"github.com/google/uuid"
)

type auditQueryFixture struct {
	tenant, resourceType string
	ids                  []string
	filter               writeaudit.Filter
	calls                int
}

func (s *auditQueryFixture) GetResourceOwners(_ context.Context, tenant, kind string, ids []string) ([]writeaudit.ResourceOwner, error) {
	s.calls++
	s.tenant = tenant
	s.resourceType = kind
	s.ids = ids
	result := make([]writeaudit.ResourceOwner, len(ids))
	for i, id := range ids {
		result[i].ResourceID = id
	}
	return result, nil
}
func (s *auditQueryFixture) ListWriteOperations(_ context.Context, tenant string, filter writeaudit.Filter) (writeaudit.Page, error) {
	s.calls++
	s.tenant = tenant
	s.filter = filter
	return writeaudit.Page{}, nil
}
func TestWriteAuditQueriesDeploymentScopeAndValidation(t *testing.T) {
	key := callerBinding()
	deps, fakes := testDependencies(t)
	queries := &auditQueryFixture{}
	fakes.writeAudit.getResourceOwners, fakes.writeAudit.listWriteOperations = queries.GetResourceOwners, queries.ListWriteOperations
	project := &projectKeyStoreFixture{project: projects.Binding{Project: projects.Project{ID: key.ProjectID}, Principal: identity.Principal{ProjectScope: identity.ProjectScope{TenantID: key.TenantID}}}}
	fakes.projectsReader.resolveAPIKey = projectKeys(t, key).ResolveAPIKey
	fakes.projectsReader.getProject = project.GetProject
	h := newTestHandler(t, deps)
	owners := "/core/v1/projects/" + key.ProjectID + "/resource-owners?resource_type=agent&resource_ids=first,second"
	for _, token := range []string{"", "caller", "foreign"} {
		w := projectKeyHTTP(h, "GET", owners, token, "")
		if w.Code != 401 || queries.calls != 0 {
			t.Fatalf("unauthorized query %d calls%d", w.Code, queries.calls)
		}
	}
	w := projectKeyHTTP(h, "GET", owners, "admin", "")
	if w.Code != 200 || queries.tenant != key.TenantID || queries.resourceType != "agent" || len(queries.ids) != 2 {
		t.Fatalf("batch %d %s", w.Code, w.Body)
	}
	if !strings.Contains(w.Body.String(), `"api_key":null`) {
		t.Fatalf("history must be null: %s", w.Body)
	}
	history := "/core/v1/projects/" + key.ProjectID + "/write-operations?"
	w = projectKeyHTTP(h, "GET", history+"key_id=some-key&resource_type=credential&resource_id=resource&limit=2&after=cursor&created_after=2026-01-01T00:00:00Z&created_before=2026-02-01T00:00:00Z", "admin", "")
	if w.Code != 200 || queries.filter.Limit != 2 || queries.filter.KeyID != "some-key" || queries.filter.After != "cursor" || queries.filter.CreatedAfter == nil || queries.filter.CreatedBefore == nil {
		t.Fatalf("history %d %s filter%+v", w.Code, w.Body, queries.filter)
	}
	for _, path := range []string{
		owners + "&resource_type=file", strings.Replace(owners, "agent", "unknown", 1), strings.Replace(owners, "first,second", "", 1),
		owners + "&tenant_id=foreign", owners + "&bad=%GG", strings.Replace(owners, "first,second", strings.Repeat("id,", 100)+"id", 1),
		history + "limit=0", history + "limit=101", history + "limit=bad", history + "limit=", history + "created_after=yesterday", history + "resource_type=unknown",
		history + "created_after=2026-02-01T00:00:00Z&created_before=2026-01-01T00:00:00Z",
	} {
		count := queries.calls
		w = projectKeyHTTP(h, "GET", path, "admin", "")
		if w.Code != 400 || queries.calls != count {
			t.Errorf("invalid query %s returned%d", path, w.Code)
		}
	}
	count := queries.calls
	w = projectKeyHTTP(h, "GET", strings.Replace(owners, key.ProjectID, "missing-project", 1), "admin", "")
	if w.Code != 404 || queries.calls != count {
		t.Fatalf("missing binding %d", w.Code)
	}
	w = projectKeyHTTP(h, "POST", owners, "admin", `{}`)
	if w.Code != 405 {
		t.Fatalf("readonly endpoint %d", w.Code)
	}
}

func TestAuthenticatedWriteProvenance(t *testing.T) {
	key := callerBinding()
	binding := projectKeyBinding(t, key)
	binding.Key = projects.APIKey{ID: uuid.NewString(), Name: "SDK", Prefix: "pc_12345678"}
	keys := &projectKeyStoreFixture{binding: binding}
	deps, fakes := testDependencies(t)
	fakes.projectsReader.resolveAPIKey = keys.ResolveAPIKey
	h := &Handler{Dependencies: deps}
	var source writeaudit.Source
	var got bool
	handler := responseHeadersWithErrors(log.HTTPMiddleware(h.authenticateCaller(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		source, got = writeaudit.FromContext(r.Context())
		w.WriteHeader(204)
	}), true)), func(string) {})
	for _, test := range []struct {
		method, path string
		present      bool
	}{{"POST", "/v1/agents", true}, {"DELETE", "/v1/files/file-one", true}, {"GET", "/v1/agents", false}, {"POST", "/core/v1/anything", false}} {
		r := httptest.NewRequest(test.method, test.path, nil)
		r.Header.Set("Authorization", "Bearer issued-project-key")
		r.Header.Set("X-API-Key-ID", "forged")
		r.Header.Set("X-Request-Id", "forged")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 204 || got != test.present {
			t.Fatalf("source eligibility %d %v", w.Code, got)
		}
		if got && (source.KeyID != binding.Key.ID || source.Kind != "issued" || source.TenantID != key.TenantID || source.RequestID != w.Header().Get("X-Request-Id") || source.RequestID == "forged" || len(source.TraceID) != 32) {
			t.Fatalf("source %+v", source)
		}
	}
	keys.resolve = projects.ErrNotFound
	if w := projectKeyHTTP(handler, "POST", "/v1/agents", "issued-project-key", ""); w.Code != 401 {
		t.Fatalf("revoked key %d", w.Code)
	}
}

func TestAuditQueryJSONSafeFields(t *testing.T) {
	now := time.Now().UTC()
	raw, err := json.Marshal(ResourceOwnerList{Data: []writeaudit.ResourceOwner{{ResourceID: "r", APIKey: &writeaudit.APIKey{ID: "key", Name: "sdk", Prefix: "pc_prefix", Kind: "issued", RevokedAt: &now}}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"id", "name", "prefix", "kind", "revoked_at"} {
		if !strings.Contains(string(raw), `"`+name+`"`) {
			t.Fatal(name)
		}
	}
	for _, name := range []string{"token_sha256", "binding_digest", "tenant_id"} {
		if strings.Contains(string(raw), name) {
			t.Fatal(name)
		}
	}
}
