package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
)

type projectKeyStoreFixture struct {
	ProjectAPIKeyStore
	binding store.ProjectAPIKeyBinding
	project store.ProjectBinding
	resolve error
	lookups int
}

func (s *projectKeyStoreFixture) GetProject(_ context.Context, id string) (store.ProjectBinding, error) {
	if s.project.Project.ID == id {
		return s.project, nil
	}
	return store.ProjectBinding{}, store.ErrNotFound
}

func (s *projectKeyStoreFixture) ResolveProjectAPIKey(_ context.Context, digest string) (store.ProjectAPIKeyBinding, error) {
	s.lookups++
	if s.resolve != nil {
		return store.ProjectAPIKeyBinding{}, s.resolve
	}
	if digest != runtimedevice.HashCredential("issued-project-key") {
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
func TestAdminCredentialNeverAuthenticatesPublicAPI(t *testing.T) {
	key := callerBinding()
	auth, err := NewAuthenticator([]APIKey{key})
	if err != nil {
		t.Fatal(err)
	}
	admin, err := NewDeploymentAuthenticator([]string{key.TokenSHA256})
	if err != nil {
		t.Fatal(err)
	}
	h := &Handler{auth: auth, deploymentAuth: admin}
	r := httptest.NewRequest("GET", "/v1/files", nil)
	r.Header.Set("Authorization", "Bearer caller")
	_, _, ok, err := h.resolveCaller(r)
	if ok || err != nil {
		t.Fatal("administrator authenticated on public API")
	}
}
func TestDatabaseResolverControlsAuthentication(t *testing.T) {
	p := callerBinding()
	fixture, _ := NewAuthenticator([]APIKey{p})
	binding, _ := fixture.keys.ResolveProjectAPIKey(t.Context(), p.TokenSHA256)
	keys := &projectKeyStoreFixture{binding: binding}
	auth, _ := NewDatabaseAuthenticator(keys)
	h := &Handler{auth: auth}
	r := httptest.NewRequest("GET", "/v1/files", nil)
	r.Header.Set("Authorization", "Bearer issued-project-key")
	got, _, ok, err := h.resolveCaller(r)
	if err != nil || !ok || got != binding.Principal {
		t.Fatal("database key rejected")
	}
	keys.resolve = store.ErrNotFound
	_, _, ok, err = h.resolveCaller(r)
	if err != nil || ok {
		t.Fatal("revoked database key authenticated")
	}
	keys.resolve = errors.New("database unavailable")
	w := projectKeyHTTP(h.authenticateCaller(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Fatal("unavailable auth reached handler") }), true), "GET", "/v1/files", "issued-project-key", "")
	if w.Code != 503 {
		t.Fatal("database failure did not fail closed")
	}
}

type separationFixture struct {
	digests []string
	err     error
}

func (s *separationFixture) ValidateProjectKeySeparation(_ context.Context, digests []string) error {
	s.digests = digests
	return s.err
}
func TestAdministratorCredentialSeparation(t *testing.T) {
	s := &separationFixture{}
	if err := ValidateCredentialSeparation(t.Context(), nil, s); err == nil {
		t.Fatal("missing administrator accepted")
	}
	digest := runtimedevice.HashCredential("admin")
	admin, _ := NewDeploymentAuthenticator([]string{digest})
	if err := ValidateCredentialSeparation(t.Context(), admin, s); err != nil || len(s.digests) != 1 || s.digests[0] != digest {
		t.Fatal("administrator digest was not checked against persisted keys", err)
	}
	s.err = errors.New("credential overlap")
	if err := ValidateCredentialSeparation(t.Context(), admin, s); !errors.Is(err, s.err) {
		t.Fatal("persisted credential collision accepted")
	}
}
func TestAdminCatalogPageLimits(t *testing.T) {
	for _, query := range []string{"limit=101", "limit=0", "limit=bad", "limit=1&limit=2", "order=sideways", "order=asc&order=desc", "after=a&after=b"} {
		if _, _, _, err := adminCatalogPage(httptest.NewRequest("GET", "/core/v1/projects?"+query, nil)); err == nil {
			t.Errorf("invalid page accepted: %s", query)
		}
	}
	_, limit, ascending, err := adminCatalogPage(httptest.NewRequest("GET", "/core/v1/projects?limit=100&order=asc", nil))
	if err != nil || limit != 100 || !ascending {
		t.Fatal("valid maximum page rejected", err)
	}
}
