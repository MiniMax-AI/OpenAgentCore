package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/providers"
)

func TestSandboxDeploymentSetupRequiresAdministratorAndStrictBody(t *testing.T) {
	deps, fakes := sandboxFakes(t)
	calls := 0
	initialize := func(_ context.Context, input sandbox.Selection) (deployment.View, error) {
		calls++
		if input.Provider == "microsandbox" {
			return deployment.View{}, deployment.ErrConflict
		}
		return deployment.View{Provider: input.Provider}, nil
	}
	fakes.deploymentChanges.initializeSandboxDeployment = initialize
	fakes.deployment.decodeConfiguration = providers.Builtin().DecodeInput
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

func TestSandboxIdlePolicyRequiresAdministratorAndStrictBody(t *testing.T) {
	deps, fakes := sandboxFakes(t)
	calls := 0
	fakes.deployment.updateIdleSeconds = func(_ context.Context, seconds int64) (deployment.View, error) {
		calls++
		if seconds < 30 || seconds > 86400 {
			return deployment.View{}, deployment.ErrInvalidInput
		}
		return deployment.View{Suspension: &deployment.Suspension{IdleSeconds: seconds, RetentionSeconds: 86400}}, nil
	}
	h := newTestHandler(t, deps)
	for _, test := range []struct {
		token, body   string
		status, calls int
	}{
		{"caller", `{"idle_seconds":60}`, 401, 0},
		{"node-credential", `{"idle_seconds":60}`, 401, 0},
		{"administrator", `{}`, 400, 0},
		{"administrator", `{"idle_seconds":null}`, 400, 0},
		{"administrator", `{"idle_seconds":30.5}`, 400, 0},
		{"administrator", `{"idle_seconds":60,"retention_seconds":1}`, 400, 0},
		{"administrator", `{"idle_seconds":29}`, 400, 1},
		{"administrator", `{"idle_seconds":86401}`, 400, 2},
		{"administrator", `{"idle_seconds":60}`, 200, 3},
	} {
		request := httptest.NewRequest(http.MethodPatch, "/core/v1/sandbox/deployment", strings.NewReader(test.body))
		request.Header.Set("Authorization", "Bearer "+test.token)
		result := httptest.NewRecorder()
		h.ServeHTTP(result, request)
		if result.Code != test.status || calls != test.calls {
			t.Fatal(result.Code, calls, result.Body.String())
		}
	}
}
