package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

func TestSandboxDeploymentChangesAuthenticateAndDecode(t *testing.T) {
	project, _ := NewAuthenticator([]APIKey{callerBinding()})
	admin, _ := NewDeploymentAuthenticator([]string{device.HashCredential("administrator")})
	updates, maintenance := 0, 0
	update := func(_ context.Context, in store.SandboxDeploymentUpdateRequest) (store.RuntimeDeploymentView, error) {
		updates++
		if in.Provider != "e2b" || in.ExpectedGeneration != 2 || in.E2B == nil || in.E2B.APIKey != "synthetic-private-key" {
			t.Fatal("write-only fields were lost")
		}
		return store.RuntimeDeploymentView{Provider: in.Provider}, nil
	}
	maintain := func(_ context.Context, in store.SandboxMaintenanceRequest) (store.RuntimeDeploymentView, error) {
		maintenance++
		if in.ExpectedGeneration != 2 {
			t.Fatal("generation was lost")
		}
		return store.RuntimeDeploymentView{Maintenance: in.Maintenance}, nil
	}
	h, err := NewHandler(&recordingStore{}, project, "codex", WithSandboxManager(&store.Store{}, admin), WithSandboxDeploymentChanges(update, maintain))
	if err != nil {
		t.Fatal(err)
	}
	const selection = `{"provider":"e2b","expected_generation":2,"e2b":{"api_key":"synthetic-private-key","template":"qualified:build"}}`
	for _, tc := range []struct {
		method, path, token, body string
		status                    int
	}{
		{"PUT", "/deployment", "caller", selection, 401},
		{"PATCH", "/deployment/maintenance", "caller", `{"maintenance":true,"expected_generation":2}`, 401},
		{"PUT", "/deployment", "administrator", strings.Replace(selection, `"api_key"`, `"API_KEY"`, 1), 400},
		{"PUT", "/deployment", "administrator", strings.Replace(selection, `"expected_generation":2,`, "", 1), 400},
		{"PUT", "/deployment", "administrator", selection, 200},
		{"PATCH", "/deployment/maintenance", "administrator", `{"expected_generation":2}`, 400},
		{"PATCH", "/deployment/maintenance", "administrator", `{"maintenance":null,"expected_generation":2}`, 400},
		{"PATCH", "/deployment/maintenance", "administrator", `{"maintenance":true,"expected_generation":2}`, 200},
		{"PATCH", "/deployment/maintenance", "administrator", `{"maintenance":false,"expected_generation":2}`, 200},
	} {
		r := httptest.NewRequest(tc.method, "/core/v1/sandbox"+tc.path, strings.NewReader(tc.body))
		r.Header.Set("Authorization", "Bearer "+tc.token)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.status || strings.Contains(w.Body.String(), "synthetic-private-key") {
			t.Fatalf("%s %s status=%d", tc.method, tc.path, w.Code)
		}
	}
	if updates != 1 || maintenance != 2 {
		t.Fatalf("unauthorized or invalid input reached mutation: %d %d", updates, maintenance)
	}
}

func TestSandboxDeploymentChangesUnavailableWithoutOwner(t *testing.T) {
	h := &Handler{}
	for _, tc := range []struct {
		body    string
		handler http.HandlerFunc
	}{
		{`{"provider":"docker","expected_generation":1}`, h.updateSandboxDeployment},
		{`{"maintenance":true,"expected_generation":1}`, h.setSandboxMaintenance},
	} {
		w := httptest.NewRecorder()
		tc.handler(w, httptest.NewRequest("PUT", "/", strings.NewReader(tc.body)))
		if w.Code != http.StatusConflict {
			t.Fatal(w.Code)
		}
	}
}
