package api

import (
	"context"
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
	resolve error
	lookups int
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
func TestIndependentIssuedPrincipalNeedsNoStaticParent(t *testing.T) {
	auth, err := NewAuthenticator(nil)
	if err != nil {
		t.Fatal(err)
	}
	p := callerBinding()
	principal, _ := NewAuthenticator([]APIKey{p})
	r := httptest.NewRequest("GET", "/v1/files", nil)
	r.Header.Set("Authorization", "Bearer caller")
	identity, _ := principal.principal(r)
	keys := &projectKeyStoreFixture{binding: store.ProjectAPIKeyBinding{Principal: identity, Key: store.ProjectAPIKey{ID: "independent"}}}
	h := &Handler{auth: auth, projectKeys: keys}
	r.Header.Set("Authorization", "Bearer issued-project-key")
	got, _, ok, err := h.resolveCaller(r)
	if err != nil || !ok || got != identity {
		t.Fatal("independent issued key rejected")
	}
}
func TestStaticKeyKindsAndIndependentSpaces(t *testing.T) {
	key := callerBinding()
	key.Kind = "console"
	if _, err := NewAuthenticator([]APIKey{key}); err == nil {
		t.Fatal("console key accepted")
	}
	key.Kind = "static"
	other := key
	other.TokenSHA256 = device.HashCredential("other")
	a, err := NewAuthenticator([]APIKey{key, other})
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateCredentialSeparation(t.Context(), a, nil, nil); err == nil {
		t.Fatal("shared static tenant accepted")
	}
}
