package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/e2b"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/providers"
)

func TestSandboxDeploymentChangesAuthenticateAndDecode(t *testing.T) {
	deps, fakes := sandboxFakes(t)
	updates, resets := 0, 0
	update := func(_ context.Context, in sandbox.Selection) (deployment.View, error) {
		updates++
		if in.Provider != "e2b" || in.ExpectedGeneration != 2 || in.Configuration == nil || in.Configuration.(*e2b.DeploymentConfiguration).APIKey != "synthetic-private-key" {
			t.Fatal("write-only fields were lost")
		}
		return deployment.View{Provider: in.Provider}, nil
	}
	maintain := func(_ context.Context, in deployment.ResetRequest) (deployment.View, error) {
		resets++
		if in.ExpectedGeneration != 2 {
			t.Fatal("generation was lost")
		}
		return deployment.View{Reset: &deployment.Reset{Clear: in.Clear}}, nil
	}
	fakes.deploymentChanges.updateSandboxDeployment, fakes.deploymentReset.startSandboxReset = update, maintain
	fakes.deployment.decodeConfiguration = providers.Builtin().DecodeInput
	fakes.deploymentReset.cancelSandboxReset = func(context.Context, uint64) (deployment.View, error) {
		return deployment.View{}, nil
	}
	h := newTestHandler(t, deps)
	const selection = `{"provider":"e2b","expected_generation":2,"credential":{"api_key":"synthetic-private-key"},"configuration":{"template":"qualified:build"}}`
	for _, tc := range []struct {
		method, path, token, body string
		status                    int
	}{
		{"PUT", "/deployment", "caller", selection, 401},
		{"PATCH", "/deployment/maintenance", "caller", `{"maintenance":true,"expected_generation":2}`, 401},
		{"PUT", "/deployment", "administrator", strings.Replace(selection, `"api_key"`, `"API_KEY"`, 1), 400},
		{"PUT", "/deployment", "administrator", strings.Replace(selection, `"expected_generation":2,`, "", 1), 400},
		{"PUT", "/deployment", "administrator", strings.Replace(selection, `"synthetic-private-key"`, `null`, 1), 400},
		{"PUT", "/deployment", "administrator", selection, 200},
		{"POST", "/deployment/reset", "administrator", `{"expected_generation":2}`, 400},
		{"POST", "/deployment/reset", "caller", `{"clear":"force","expected_generation":2}`, 401},
		{"POST", "/deployment/reset", "administrator", `{"clear":"force","deadline_seconds":null,"expected_generation":2}`, 400},
		{"POST", "/deployment/reset", "administrator", `{"clear":"auto","expected_generation":null}`, 400},
		{"DELETE", "/deployment/reset", "administrator", "", 400},
		{"DELETE", "/deployment/reset?expected_generation=2&expected_generation=2", "administrator", "", 400},
		{"POST", "/deployment/reset", "administrator", `{"clear":"auto","deadline_seconds":299,"expected_generation":2}`, 400},
		{"PATCH", "/deployment/maintenance", "administrator", `{"maintenance":true,"expected_generation":2}`, 404},
		{"POST", "/deployment/reset", "administrator", `{"clear":"auto","expected_generation":2}`, 200},
		{"POST", "/deployment/reset", "administrator", `{"clear":"force","expected_generation":2}`, 200},
		{"DELETE", "/deployment/reset?expected_generation=2", "administrator", "", 200},
	} {
		r := httptest.NewRequest(tc.method, "/core/v1/sandbox"+tc.path, strings.NewReader(tc.body))
		r.Header.Set("Authorization", "Bearer "+tc.token)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.status || strings.Contains(w.Body.String(), "synthetic-private-key") {
			t.Fatalf("%s %s status=%d", tc.method, tc.path, w.Code)
		}
	}
	if updates != 1 || resets != 2 {
		t.Fatalf("unauthorized or invalid input reached mutation: %d %d", updates, resets)
	}
}

// Deployment errors carry typed details only through the Core router.
func TestSandboxMutationErrorsExposeOnlyTypedCoreFacts(t *testing.T) {
	for _, tc := range []struct {
		err     error
		code    string
		status  int
		details string
	}{
		{&deployment.GenerationStaleError{CurrentGeneration: 8}, "sandbox_generation_stale", 409, `"current_generation":8`},
		{&deployment.ResetRequiredError{CurrentProvider: "microsandbox", RequestedProvider: "e2b"}, "sandbox_reset_required", 409, `"requested_provider":"e2b"`},
		{&deployment.ResetRequiredError{CurrentProvider: "e2b", RequestedProvider: "e2b"}, "sandbox_reset_required", 409, `"current_provider":"e2b"`},
		{&deployment.InUseError{Resources: deployment.Resources{Allocations: 2, Pending: 1}}, "sandbox_in_use", 409, `"allocations":2`},
		{deployment.ErrResetInProgress, "sandbox_reset_in_progress", 409, ""},
	} {
		for _, core := range []bool{false, true} {
			handler := http.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { writeDeploymentError(w, r, tc.err) }))
			if core {
				handler = coreErrorResponses(handler)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest("POST", "/test", nil))
			body := response.Body.String()
			if response.Code != tc.status || !strings.Contains(body, `"code":"`+tc.code+`"`) {
				t.Fatal(body)
			}
			if core && tc.details != "" && !strings.Contains(body, tc.details) {
				t.Fatal("typed detail missing", body)
			}
			if !core && strings.Contains(body, `"details"`) {
				t.Fatal("Core facts escaped their router", body)
			}
		}
	}
}

// An omitted network is the default cap and is marked so an update keeps the
// current one; a submitted network reaches the selection with its domains.
func TestSandboxDeploymentNetworkSelection(t *testing.T) {
	deps, fakes := sandboxFakes(t)
	var got sandbox.Selection
	fakes.deploymentChanges.updateSandboxDeployment = func(_ context.Context, in sandbox.Selection) (deployment.View, error) {
		got = in
		return deployment.View{Provider: in.Provider}, nil
	}
	fakes.deployment.decodeConfiguration = providers.Builtin().DecodeInput
	h := newTestHandler(t, deps)
	const base = `{"provider":"e2b","expected_generation":2,"credential":{"api_key":"synthetic-private-key"},"configuration":{"template":"qualified:build"}`
	for _, tc := range []struct {
		body    string
		network sandbox.Network
		omitted bool
	}{
		{base + `}`, sandbox.DefaultNetwork(), true},
		{base + `,"network":{"public":false,"domains":null}}`, sandbox.Network{Domains: []string{}}, false},
		{base + `,"network":{"public":true,"domains":["gitlab.example.com"]}}`, sandbox.Network{Public: true, Domains: []string{"gitlab.example.com"}}, false},
	} {
		r := httptest.NewRequest("PUT", "/core/v1/sandbox/deployment", strings.NewReader(tc.body))
		r.Header.Set("Authorization", "Bearer administrator")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 200 || !reflect.DeepEqual(got.Network, tc.network) || got.NetworkOmitted != tc.omitted {
			t.Fatalf("%s: status=%d network=%+v omitted=%v", tc.body, w.Code, got.Network, got.NetworkOmitted)
		}
	}
}

// A submitted network without public or domains is a 400 naming the member.
func TestSandboxDeploymentNetworkRequiresMembers(t *testing.T) {
	deps, _ := sandboxFakes(t)
	h := newTestHandler(t, deps)
	const base = `{"provider":"e2b","expected_generation":2,"configuration":{"template":"qualified:build"}`
	for _, tc := range []struct{ network, param string }{
		{`{}`, "network.public"},
		{`{"domains":[]}`, "network.public"},
		{`{"public":true}`, "network.domains"},
	} {
		for _, method := range []string{"POST", "PUT"} {
			r := httptest.NewRequest(method, "/core/v1/sandbox/deployment", strings.NewReader(base+`,"network":`+tc.network+`}`))
			r.Header.Set("Authorization", "Bearer administrator")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != 400 || !strings.Contains(w.Body.String(), tc.param) {
				t.Fatalf("%s %s: status=%d %s", method, tc.network, w.Code, w.Body.String())
			}
		}
	}
}
