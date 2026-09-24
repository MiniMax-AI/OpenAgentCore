package api

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
	"github.com/MiniMax-AI-Dev/parsar/internal/obs/log"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/writeaudit"
	"github.com/google/uuid"
)

type auditQueryFixture struct {
	tenant, resourceType string
	ids                  []string
	filter               store.WriteOperationFilter
	calls                int
}

func (s *auditQueryFixture) GetResourceOwners(_ context.Context, tenant, kind string, ids []string) ([]store.ResourceOwner, error) {
	s.calls++
	s.tenant = tenant
	s.resourceType = kind
	s.ids = ids
	result := make([]store.ResourceOwner, len(ids))
	for i, id := range ids {
		result[i].ResourceID = id
	}
	return result, nil
}
func (s *auditQueryFixture) ListWriteOperations(_ context.Context, tenant string, filter store.WriteOperationFilter) (store.WriteOperationPage, error) {
	s.calls++
	s.tenant = tenant
	s.filter = filter
	return store.WriteOperationPage{}, nil
}
func TestWriteAuditQueriesDeploymentScopeAndValidation(t *testing.T) {
	key := callerBinding()
	auth, err := NewAuthenticator([]APIKey{key})
	if err != nil {
		t.Fatal(err)
	}
	admin, err := NewDeploymentAuthenticator([]string{device.HashCredential("admin")})
	if err != nil {
		t.Fatal(err)
	}
	queries := &auditQueryFixture{}
	h, err := NewHandler(&recordingStore{}, auth, "codex", WithWriteAudit(queries, admin))
	if err != nil {
		t.Fatal(err)
	}
	owners := "/core/v1/resource-owners?binding_digest=" + key.TokenSHA256 + "&resource_type=agent&resource_ids=first,second"
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
	history := "/core/v1/write-operations?binding_digest=" + key.TokenSHA256
	w = projectKeyHTTP(h, "GET", history+"&key_id=some-key&resource_type=credential&resource_id=resource&limit=2&after=cursor&created_after=2026-01-01T00:00:00Z&created_before=2026-02-01T00:00:00Z", "admin", "")
	if w.Code != 200 || queries.filter.Limit != 2 || queries.filter.KeyID != "some-key" || queries.filter.After != "cursor" || queries.filter.CreatedAfter == nil || queries.filter.CreatedBefore == nil {
		t.Fatalf("history %d %s filter%+v", w.Code, w.Body, queries.filter)
	}
	for _, path := range []string{
		owners + "&resource_type=file", strings.Replace(owners, "agent", "unknown", 1), strings.Replace(owners, "first,second", "", 1),
		owners + "&tenant_id=foreign", owners + "&bad=%GG", strings.Replace(owners, "first,second", strings.Repeat("id,", 100)+"id", 1),
		history + "&limit=0", history + "&limit=101", history + "&limit=bad", history + "&limit=", history + "&created_after=yesterday", history + "&resource_type=unknown",
		history + "&created_after=2026-02-01T00:00:00Z&created_before=2026-01-01T00:00:00Z",
	} {
		count := queries.calls
		w = projectKeyHTTP(h, "GET", path, "admin", "")
		if w.Code != 400 || queries.calls != count {
			t.Errorf("invalid query %s returned%d", path, w.Code)
		}
	}
	count := queries.calls
	w = projectKeyHTTP(h, "GET", strings.Replace(owners, key.TokenSHA256, device.HashCredential("absent"), 1), "admin", "")
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
	key.Name = "Console"
	key.Kind = "console"
	auth, err := NewAuthenticator([]APIKey{key})
	if err != nil {
		t.Fatal(err)
	}
	principal := auth.principals[[32]byte(mustDigestForAudit(t, key.TokenSHA256))]
	issued := store.ProjectAPIKey{ID: uuid.NewString(), Name: "SDK", Prefix: "pc_12345678"}
	keys := &projectKeyStoreFixture{binding: store.ProjectAPIKeyBinding{BindingDigest: key.TokenSHA256, Principal: principal, Key: issued}}
	h := &Handler{auth: auth, projectKeys: keys}
	var source writeaudit.Source
	var got bool
	handler := agentsResponseHeaders(log.HTTPMiddleware(h.authenticateCaller(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		source, got = writeaudit.FromContext(r.Context())
		w.WriteHeader(204)
	}), true)))
	for _, test := range []struct {
		method, path, token, kind, id string
		present                       bool
	}{
		{"POST", "/v1/agents", "caller", "console", "static:" + key.TokenSHA256, true},
		{"DELETE", "/v1/files/file-one", "issued-project-key", "issued", issued.ID, true},
		{"GET", "/v1/agents", "caller", "", "", false},
		{"POST", "/core/v1/anything", "caller", "", "", false},
	} {
		r := httptest.NewRequest(test.method, test.path, nil)
		r.Header.Set("Authorization", "Bearer "+test.token)
		r.Header.Set("X-API-Key-ID", "forged")
		r.Header.Set("X-Request-Id", "forged")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 204 || got != test.present {
			t.Fatalf("source eligibility%d %v", w.Code, got)
		}
		if got && (source.KeyID != test.id || source.Kind != test.kind || source.TenantID != key.TenantID || source.RequestID != w.Header().Get("X-Request-Id") || source.RequestID == "forged" || len(source.TraceID) != 32) {
			t.Fatalf("source %+v", source)
		}
	}
	keys.resolve = store.ErrNotFound
	w := projectKeyHTTP(handler, "POST", "/v1/agents", "issued-project-key", "")
	if w.Code != 401 {
		t.Fatalf("revoked key %d", w.Code)
	}
}

func mustDigestForAudit(t *testing.T, s string) []byte {
	t.Helper()
	out, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestStaticAuditIdentityStableAcrossDisplayChange(t *testing.T) {
	key := callerBinding()
	a, err := NewAuthenticator([]APIKey{key})
	if err != nil {
		t.Fatal(err)
	}
	key.Name = "Renamed"
	key.Kind = "console"
	b, err := NewAuthenticator([]APIKey{key})
	if err != nil {
		t.Fatal(err)
	}
	for hash, source := range a.sources {
		if source.KeyID != b.sources[hash].KeyID || source.Prefix != b.sources[hash].Prefix {
			t.Fatal("display change replaced key identity")
		}
	}
	for _, edit := range []func(*APIKey){func(k *APIKey) { k.Kind = "issued" }, func(k *APIKey) { k.Name = "bad\nname" }, func(k *APIKey) { k.Name = strings.Repeat("a", 81) }} {
		bad := key
		edit(&bad)
		if _, err := NewAuthenticator([]APIKey{bad}); err == nil {
			t.Fatal("invalid audit config accepted")
		}
	}
}

func TestAuditQueryJSONSafeFields(t *testing.T) {
	now := time.Now().UTC()
	raw, err := json.Marshal(ResourceOwnerList{Data: []store.ResourceOwner{{ResourceID: "r", APIKey: &store.AuditAPIKey{ID: "key", Name: "sdk", Prefix: "pc_prefix", Kind: "issued", RevokedAt: &now}}}})
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
