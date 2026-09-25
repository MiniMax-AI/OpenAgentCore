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

func TestSandboxDeploymentSetupRequiresAdministratorAndStrictBody(t *testing.T) {
	project, _ := NewAuthenticator([]APIKey{callerBinding()})
	admin, _ := NewDeploymentAuthenticator([]string{device.HashCredential("administrator")})
	calls := 0
	initialize := func(_ context.Context, input store.SandboxDeploymentSetupRequest) (store.RuntimeDeploymentView, error) {
		calls++
		if input.Provider == "microsandbox" {
			return store.RuntimeDeploymentView{}, store.ErrSandboxDeploymentConflict
		}
		return store.RuntimeDeploymentView{Provider: input.Provider}, nil
	}
	h, err := NewHandler(&recordingStore{}, project, "codex", WithSandboxManager(&store.Store{}, admin), WithSandboxDeploymentSetup(initialize))
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		token, body   string
		status, calls int
	}{
		{"caller", `{"provider":"docker"}`, 401, 0},
		{"node-credential", `{"provider":"docker"}`, 401, 0},
		{"enrollment-token", `{"provider":"docker"}`, 401, 0},
		{"administrator", `{"provider":"docker","unexpected":true}`, 400, 0},
		{"administrator", `{"provider":"docker"}`, 200, 1},
		{"administrator", `{"provider":"microsandbox"}`, 409, 2},
	} {
		request := httptest.NewRequest(http.MethodPost, "/core/v1/sandbox/deployment", strings.NewReader(test.body))
		request.Header.Set("Authorization", "Bearer "+test.token)
		result := httptest.NewRecorder()
		h.ServeHTTP(result, request)
		if result.Code != test.status || calls != test.calls {
			t.Fatal(result.Code, calls, result.Body.String())
		}
	}
}

func TestSandboxDeploymentSetupFileModeReturnsConflict(t *testing.T) {
	h := &Handler{}
	request := httptest.NewRequest(http.MethodPost, "/core/v1/sandbox/deployment", strings.NewReader(`{"provider":"docker"}`))
	result := httptest.NewRecorder()
	h.initializeSandboxDeployment(result, request)
	if result.Code != http.StatusConflict || !strings.Contains(result.Body.String(), "sandbox_deployment_conflict") {
		t.Fatal(result.Code, result.Body.String())
	}
}
