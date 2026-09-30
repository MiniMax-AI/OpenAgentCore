package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
)

func TestSandboxDeploymentSetupRequiresAdministratorAndStrictBody(t *testing.T) {
	deps, fakes := sandboxFakes(t)
	calls := 0
	initialize := func(_ context.Context, input store.SandboxDeploymentSetupRequest) (store.RuntimeDeploymentView, error) {
		calls++
		if input.Provider == "microsandbox" {
			return store.RuntimeDeploymentView{}, store.ErrSandboxDeploymentConflict
		}
		return store.RuntimeDeploymentView{Provider: input.Provider}, nil
	}
	fakes.deploymentChanges.initializeSandboxDeployment = initialize
	h := newTestHandler(t, deps)
	for _, test := range []struct {
		token, body   string
		status, calls int
	}{
		{"caller", `{"provider":"docker","expected_generation":0}`, 401, 0},
		{"node-credential", `{"provider":"docker","expected_generation":0}`, 401, 0},
		{"enrollment-token", `{"provider":"docker","expected_generation":0}`, 401, 0},
		{"administrator", `{"provider":"docker","unexpected":true}`, 400, 0},
		{"administrator", `{"provider":"docker"}`, 400, 0},
		{"administrator", `{"provider":"docker","expected_generation":null}`, 400, 0},
		{"administrator", `{"provider":"docker","expected_generation":0}`, 200, 1},
		{"administrator", `{"provider":"microsandbox","expected_generation":0}`, 409, 2},
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
