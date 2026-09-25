package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

func TestSandboxAdministratorIsSeparateFromProject(t *testing.T) {
	project, err := NewAuthenticator([]APIKey{callerBinding()})
	if err != nil {
		t.Fatal(err)
	}
	admin, err := NewDeploymentAuthenticator([]string{device.HashCredential("administrator")})
	if err != nil {
		t.Fatal(err)
	}
	h, err := NewHandler(&recordingStore{}, project, "codex", WithSandboxManager(&store.Store{}, admin))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/core/v1/sandbox/nodes", "/core/v1/sandbox/deployment", "/core/v1/sandbox/nodes/node/allocations", "/core/v1/sandbox/enrollment-tokens"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		if strings.HasSuffix(path, "enrollment-tokens") {
			request.Method = http.MethodPost
		}
		request.Header.Set("Authorization", "Bearer caller")
		result := httptest.NewRecorder()
		h.ServeHTTP(result, request)
		if result.Code != http.StatusUnauthorized || !strings.Contains(result.Body.String(), "invalid_admin_key") {
			t.Fatal(path, result.Code, result.Body.String())
		}
	}
	request := httptest.NewRequest(http.MethodGet, "/v1/agents/sessions", nil)
	request.Header.Set("Authorization", "Bearer administrator")
	request.Header.Set("OpenAI-Beta", "agents=v1")
	result := httptest.NewRecorder()
	h.ServeHTTP(result, request)
	if result.Code != http.StatusUnauthorized {
		t.Fatal("admin key gained project authority", result.Code)
	}
	reused, _ := NewDeploymentAuthenticator([]string{device.HashCredential("caller")})
	collided, err := NewHandler(&recordingStore{}, project, "codex", WithSandboxManager(&store.Store{}, reused))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer caller")
	result = httptest.NewRecorder()
	collided.ServeHTTP(result, request)
	if result.Code != http.StatusUnauthorized {
		t.Fatal("administrator digest collision gained public authority", result.Code)
	}
}
func TestSandboxEnrollmentDoesNotAcceptProjectAsAdmin(t *testing.T) {
	auth, _ := NewDeploymentAuthenticator([]string{device.HashCredential("administrator")})
	called := false
	protected := auth.authenticate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true; w.WriteHeader(http.StatusNoContent) }))
	for _, token := range []string{"caller", "node-credential", "enrollment-token", ""} {
		request := httptest.NewRequest(http.MethodDelete, "/core/v1/sandbox/nodes/node", nil)
		request.Header.Set("Authorization", "Bearer "+token)
		result := httptest.NewRecorder()
		protected.ServeHTTP(result, request)
		if called || result.Code != 401 {
			t.Fatal("non-admin credential admitted")
		}
	}
}

func TestSandboxLocalNodeRemovalExplainsDeploymentBinding(t *testing.T) {
	request := httptest.NewRequest(http.MethodDelete, "/core/v1/sandbox/nodes/local", nil)
	response := httptest.NewRecorder()
	writeStoreError(response, request, store.ErrRuntimeLocalNodeConfigured)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "runtime_local_node_configured") || !strings.Contains(response.Body.String(), "maintenance") {
		t.Fatal(response.Code, response.Body.String())
	}
}

func TestSandboxEnrollmentCapacityIsAdministratorOnly(t *testing.T) {
	project, _ := NewAuthenticator([]APIKey{callerBinding()})
	admin, _ := NewDeploymentAuthenticator([]string{device.HashCredential("administrator")})
	h, err := NewHandler(&recordingStore{}, project, "codex", WithSandboxManager(&store.Store{}, admin))
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ path, token, body string }{
		{"/core/v1/sandbox/enrollment-tokens", "administrator", `{"max_active":0}`},
		{"/core/v1/sandbox/enrollment-tokens", "administrator", `{"max_active":9,"max_retained":8}`},
		{"/core/v1/sandbox/enrollment-tokens", "administrator", `{"max_retained":1000001}`},
		{"/core/v1/sandbox/enroll", "one-use", `{"max_active":100}`},
		{"/core/v1/sandbox/enroll", "one-use", `{"max_retained":100}`},
	} {
		request := httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(test.body))
		request.Header.Set("Authorization", "Bearer "+test.token)
		response := httptest.NewRecorder()
		h.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatal(test.path, response.Code, response.Body.String())
		}
	}
}
