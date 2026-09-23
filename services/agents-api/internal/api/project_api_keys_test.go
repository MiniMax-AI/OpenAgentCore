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

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/identity"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

type projectKeyStoreFixture struct {
	keys      map[string]store.ProjectAPIKey
	binding   store.ProjectAPIKeyBinding
	resolve   error
	lookups   int
	creates   int
	createdBy identity.Principal
}

func (s *projectKeyStoreFixture) CreateProjectAPIKey(_ context.Context, binding string, principal identity.Principal, id, name string) (store.IssuedProjectAPIKey, error) {
	s.creates++
	if _, exists := s.keys[id]; exists {
		return store.IssuedProjectAPIKey{}, store.ErrProjectAPIKeyExists
	}
	key := store.ProjectAPIKey{ID: id, Name: name, Prefix: "test-prefix", CreatedAt: time.Unix(1700000000, 0).UTC()}
	s.keys[id] = key
	s.binding = store.ProjectAPIKeyBinding{BindingDigest: binding, Principal: principal}
	s.createdBy = principal
	return store.IssuedProjectAPIKey{ProjectAPIKey: key, Key: "issued-project-key"}, nil
}

func (s *projectKeyStoreFixture) ListProjectAPIKeys(_ context.Context, binding string, principal identity.Principal) ([]store.ProjectAPIKey, error) {
	if binding != s.binding.BindingDigest || principal != s.binding.Principal {
		return nil, nil
	}
	var result []store.ProjectAPIKey
	for _, key := range s.keys {
		result = append(result, key)
	}
	return result, nil
}

func (s *projectKeyStoreFixture) RevokeProjectAPIKey(_ context.Context, binding string, principal identity.Principal, id string) error {
	if _, ok := s.keys[id]; !ok || binding != s.binding.BindingDigest || principal != s.binding.Principal {
		return store.ErrNotFound
	}
	key := s.keys[id]
	now := time.Now()
	key.RevokedAt = &now
	s.keys[id] = key
	s.resolve = store.ErrNotFound
	return nil
}

func (s *projectKeyStoreFixture) ResolveProjectAPIKey(_ context.Context, digest string) (store.ProjectAPIKeyBinding, error) {
	s.lookups++
	if s.resolve != nil {
		return store.ProjectAPIKeyBinding{}, s.resolve
	}
	if digest != device.HashCredential("issued-project-key") {
		return store.ProjectAPIKeyBinding{}, store.ErrNotFound
	}
	return s.binding, nil
}

func projectKeyHTTP(h http.Handler, method, path, token, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("OpenAI-Beta", "agents=v1")
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func projectKeyHandler(t *testing.T) (http.Handler, *Authenticator, *projectKeyStoreFixture, *recordingStore, APIKey) {
	t.Helper()
	parent := callerBinding()
	auth, err := NewAuthenticator([]APIKey{parent})
	if err != nil {
		t.Fatal(err)
	}
	admin, err := NewDeploymentAuthenticator([]string{device.HashCredential("deployment-admin")})
	if err != nil {
		t.Fatal(err)
	}
	keys := &projectKeyStoreFixture{keys: map[string]store.ProjectAPIKey{}}
	resources := &recordingStore{}
	h, err := NewHandler(resources, auth, "codex", WithSandboxManager(&store.Store{}, admin), WithProjectAPIKeys(keys, admin))
	if err != nil {
		t.Fatal(err)
	}
	return h, auth, keys, resources, parent
}

func TestProjectAPIKeyManagementUsesOnlyDeploymentAuthorityAndStaticBinding(t *testing.T) {
	h, _, keys, resources, parent := projectKeyHandler(t)
	base := "/core/v1/project-api-keys/" + parent.TokenSHA256
	id := uuid.NewString()
	body := `{"id":"` + id + `","name":" My first key "}`
	for _, token := range []string{"caller", "node-credential", "issued-project-key", "", "unknown"} {
		for _, method := range []string{"GET", "POST", "DELETE"} {
			path := base
			if method == "DELETE" {
				path += "/" + id
			}
			w := projectKeyHTTP(h, method, path, token, body)
			if w.Code != 401 {
				t.Errorf("%s accepted non-admin credential: %d", method, w.Code)
			}
		}
	}
	if keys.creates != 0 || keys.lookups != 0 {
		t.Fatal("management admitted project or node authority")
	}
	w := projectKeyHTTP(h, "POST", base, "deployment-admin", body)
	if w.Code != 201 || !strings.Contains(w.Body.String(), `"key":"issued-project-key"`) {
		t.Fatalf("create failed: %d %s", w.Code, w.Body)
	}
	if keys.createdBy.TenantID != parent.TenantID || keys.createdBy.SubjectID != parent.SubjectID || keys.keys[id].Name != "My first key" {
		t.Fatal("created key did not inherit the exact static principal")
	}
	for _, path := range []string{base, "/core/v1/project-api-keys/" + device.HashCredential("issued-project-key")} {
		w := projectKeyHTTP(h, "GET", path, "deployment-admin", "")
		if path == base {
			if w.Code != 200 || !strings.Contains(w.Body.String(), `"has_more":false`) || strings.Contains(w.Body.String(), "issued-project-key") || strings.Contains(w.Body.String(), parent.TokenSHA256) {
				t.Fatal("list leaked key material or omitted safe metadata")
			}
		} else if w.Code != 404 {
			t.Fatal("dynamic key digest was accepted as a parent binding")
		}
	}
	if w := projectKeyHTTP(h, "POST", base, "deployment-admin", body); w.Code != 409 || strings.Contains(w.Body.String(), "issued-project-key") {
		t.Fatal("duplicate create replayed the secret or failed to conflict")
	}
	if w := projectKeyHTTP(h, "GET", "/v1/agents/sessions", "issued-project-key", ""); w.Code != 200 || resources.listTenant != parent.TenantID {
		t.Fatalf("issued key not accepted as inherited project principal: %d", w.Code)
	}
	if w := projectKeyHTTP(h, "GET", "/core/v1/sandbox/nodes", "issued-project-key", ""); w.Code != 401 {
		t.Fatal("issued project key gained deployment management authority")
	}
	if w := projectKeyHTTP(h, "DELETE", base+"/"+uuid.NewString(), "deployment-admin", ""); w.Code != 404 {
		t.Fatal("missing revoke did not return 404")
	}
	if w := projectKeyHTTP(h, "DELETE", base+"/"+id, "deployment-admin", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"deleted":true`) {
		t.Fatal("revoke failed")
	}
	if w := projectKeyHTTP(h, "GET", "/v1/agents/sessions", "issued-project-key", ""); w.Code != 401 {
		t.Fatal("revoked key was retained in an authentication cache")
	}
	if w := projectKeyHTTP(h, "GET", "/v1/agents/sessions", "caller", ""); w.Code != 200 {
		t.Fatal("revoking a child disabled its static parent")
	}
}

func TestDynamicProjectKeyScopeRebindingAndDatabaseFailure(t *testing.T) {
	h, auth, keys, _, parent := projectKeyHandler(t)
	base := "/core/v1/project-api-keys/" + parent.TokenSHA256
	body, _ := json.Marshal(ProjectAPIKeyRequest{ID: uuid.NewString(), Name: "test"})
	if w := projectKeyHTTP(h, "POST", base, "deployment-admin", string(body)); w.Code != 201 {
		t.Fatal("could not create fixture key")
	}
	for _, headers := range []http.Header{
		{"Openai-Organization": {"other"}}, {"Openai-Project": {"other"}},
		{"Openai-Project": {parent.ProjectID, parent.ProjectID}},
		{"Authorization": {"Bearer issued-project-key", "Bearer caller"}},
	} {
		r := httptest.NewRequest("GET", "/v1/agents/sessions", nil)
		r.Header.Set("Authorization", "Bearer issued-project-key")
		r.Header.Set("OpenAI-Beta", "agents=v1")
		for name, values := range headers {
			r.Header[name] = values
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 401 {
			t.Fatal("derived key bypassed principal header checks")
		}
	}
	keys.resolve = errors.New("private database details")
	before := keys.lookups
	if w := projectKeyHTTP(h, "GET", "/v1/agents/sessions", "caller", ""); w.Code != 200 || keys.lookups != before {
		t.Fatal("static authentication acquired a database dependency")
	}
	if w := projectKeyHTTP(h, "GET", "/v1/agents/sessions", "issued-project-key", ""); w.Code != 503 || strings.Contains(w.Body.String(), "private database details") {
		t.Fatal("database failure granted access, misreported credentials or leaked details")
	}
	keys.resolve = nil
	for digest, principal := range auth.principals {
		changed := principal
		changed.SubjectID = "different-subject"
		auth.principals[digest] = changed
		if w := projectKeyHTTP(h, "GET", "/v1/agents/sessions", "issued-project-key", ""); w.Code != 401 {
			t.Fatal("static rebinding silently changed child key authority")
		}
		delete(auth.principals, digest)
		if w := projectKeyHTTP(h, "GET", "/v1/agents/sessions", "issued-project-key", ""); w.Code != 401 {
			t.Fatal("removed static parent left child key enabled")
		}
	}
}

func TestProjectAPIKeyManagementRejectsPrincipalOverridesAndMalformedRequests(t *testing.T) {
	h, _, keys, _, parent := projectKeyHandler(t)
	base := "/core/v1/project-api-keys/" + parent.TokenSHA256
	for _, body := range []string{
		`{"id":"invalid","name":"name"}`,
		`{"id":"` + uuid.NewString() + `","name":""}`,
		`{"id":"` + uuid.NewString() + `","name":"name","tenant_id":"forged"}`,
		`{"id":"` + uuid.NewString() + `","name":"name","binding_digest":"forged"}`,
		`{"id":"` + uuid.NewString() + `","name":"bad\nname"}`,
		`{"id":"` + uuid.NewString() + `","name":"` + strings.Repeat("x", 81) + `"}`,
	} {
		if w := projectKeyHTTP(h, "POST", base, "deployment-admin", body); w.Code != 400 {
			t.Errorf("invalid key request returned %d", w.Code)
		}
	}
	if w := projectKeyHTTP(h, "GET", base+"?binding_digest=other", "deployment-admin", ""); w.Code != 400 {
		t.Fatal("query override accepted")
	}
	if w := projectKeyHTTP(h, "GET", "/core/v1/project-api-keys/invalid", "deployment-admin", ""); w.Code != 404 {
		t.Fatal("unknown binding accepted")
	}
	if keys.creates != 0 {
		t.Fatal("invalid key request reached storage")
	}
}
