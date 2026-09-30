package api

import (
	"context"
	"crypto/sha256"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/projects"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
)

type projectKeyStoreFixture struct {
	binding projects.KeyBinding
	project projects.Binding
	resolve error
	lookups int
}

func (s *projectKeyStoreFixture) GetProject(_ context.Context, id string) (projects.Binding, error) {
	if s.project.Project.ID == id {
		return s.project, nil
	}
	return projects.Binding{}, projects.ErrNotFound
}

func (s *projectKeyStoreFixture) ResolveAPIKey(_ context.Context, digest [sha256.Size]byte) (projects.KeyBinding, error) {
	s.lookups++
	if s.resolve != nil {
		return projects.KeyBinding{}, s.resolve
	}
	if digest != sha256.Sum256([]byte("issued-project-key")) {
		return projects.KeyBinding{}, projects.ErrNotFound
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
	deps, fakes := testDependencies(t)
	fakes.projectsReader.resolveAPIKey = projectKeys(t, key).ResolveAPIKey
	deps.CoreKeys = coreKeys(t, "caller")
	h := &Handler{Dependencies: deps}
	r := httptest.NewRequest("GET", "/v1/files", nil)
	r.Header.Set("Authorization", "Bearer caller")
	_, _, ok, err := h.resolveCaller(r)
	if ok || err != nil {
		t.Fatal("administrator authenticated on public API")
	}
}
func TestDatabaseResolverControlsAuthentication(t *testing.T) {
	p := callerBinding()
	binding := projectKeyBinding(t, p)
	keys := &projectKeyStoreFixture{binding: binding}
	deps, fakes := testDependencies(t)
	fakes.projectsReader.resolveAPIKey = keys.ResolveAPIKey
	h := &Handler{Dependencies: deps}
	r := httptest.NewRequest("GET", "/v1/files", nil)
	r.Header.Set("Authorization", "Bearer issued-project-key")
	got, _, ok, err := h.resolveCaller(r)
	if err != nil || !ok || got != binding.Principal {
		t.Fatal("database key rejected")
	}
	keys.resolve = projects.ErrNotFound
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
	checked [][sha256.Size]byte
	exists  bool
	err     error
}

func (s *separationFixture) APIKeyDigestExists(_ context.Context, digest [sha256.Size]byte) (bool, error) {
	s.checked = append(s.checked, digest)
	return s.exists, s.err
}
func TestAdministratorCredentialSeparation(t *testing.T) {
	s := &separationFixture{}
	if err := ValidateCredentialSeparation(t.Context(), nil, s); err == nil {
		t.Fatal("missing administrator accepted")
	}
	admin, _ := NewDeploymentAuthenticator([]string{runtimedevice.HashCredential("admin")})
	if err := ValidateCredentialSeparation(t.Context(), admin, s); err != nil || len(s.checked) != 1 || s.checked[0] != sha256.Sum256([]byte("admin")) {
		t.Fatal("administrator digest was not checked against persisted keys", err)
	}
	s.exists = true
	if err := ValidateCredentialSeparation(t.Context(), admin, s); err == nil {
		t.Fatal("persisted credential collision accepted")
	}
	s.exists, s.err = false, errors.New("database unavailable")
	if err := ValidateCredentialSeparation(t.Context(), admin, s); !errors.Is(err, s.err) {
		t.Fatal("digest lookup failure ignored")
	}
}
func TestAdminCatalogPageLimits(t *testing.T) {
	for _, query := range []string{"limit=101", "limit=0", "limit=bad", "limit=1&limit=2", "order=sideways", "order=asc&order=desc", "after=a&after=b"} {
		if _, err := adminCatalogPage(httptest.NewRequest("GET", "/core/v1/projects?"+query, nil)); err == nil {
			t.Errorf("invalid page accepted: %s", query)
		}
	}
	page, err := adminCatalogPage(httptest.NewRequest("GET", "/core/v1/projects?limit=100&order=asc", nil))
	if err != nil || page.Limit != 100 || !page.Ascending {
		t.Fatal("valid maximum page rejected", err)
	}
}
