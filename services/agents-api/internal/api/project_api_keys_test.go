package api

import (
	"context"
	"errors"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
