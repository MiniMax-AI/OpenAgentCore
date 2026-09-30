package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/e2b"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/device"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
)

func TestSandboxDeploymentChangesAuthenticateAndDecode(t *testing.T) {
	project, _ := NewAuthenticator([]APIKey{callerBinding()})
	admin, _ := NewDeploymentAuthenticator([]string{device.HashCredential("administrator")})
	updates, resets := 0, 0
	update := func(_ context.Context, in store.SandboxDeploymentUpdateRequest) (store.RuntimeDeploymentView, error) {
		updates++
		if in.Provider != "e2b" || in.ExpectedGeneration != 2 || in.Configuration == nil || in.Configuration.(*e2b.DeploymentConfiguration).APIKey != "synthetic-private-key" {
			t.Fatal("write-only fields were lost")
		}
		return store.RuntimeDeploymentView{Provider: in.Provider}, nil
	}
	maintain := func(_ context.Context, in store.SandboxResetRequest) (store.RuntimeDeploymentView, error) {
		resets++
		if in.ExpectedGeneration != 2 {
			t.Fatal("generation was lost")
		}
		return store.RuntimeDeploymentView{Reset: &store.SandboxResetView{Clear: in.Clear}}, nil
	}
	h, err := NewHandler(&recordingStore{}, project, "codex", WithSandboxManager(&store.Store{}, admin), WithSandboxDeploymentChanges(update, maintain, func(context.Context, uint64) (store.RuntimeDeploymentView, error) {
		return store.RuntimeDeploymentView{}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
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

func TestSandboxDeploymentChangesUnavailableWithoutOwner(t *testing.T) {
	h := &Handler{}
	for _, tc := range []struct {
		body    string
		handler http.HandlerFunc
	}{
		{`{"provider":"docker","expected_generation":1}`, h.updateSandboxDeployment},
		{`{"clear":"auto","expected_generation":1}`, h.startSandboxReset},
	} {
		w := httptest.NewRecorder()
		tc.handler(w, httptest.NewRequest("PUT", "/", strings.NewReader(tc.body)))
		if w.Code != http.StatusConflict {
			t.Fatal(w.Code)
		}
	}
}

func TestSandboxMutationErrorsExposeOnlyTypedCoreFacts(t *testing.T) {
	for _, tc := range []struct {
		err     error
		code    string
		status  int
		details string
	}{
		{&store.SandboxGenerationStaleError{CurrentGeneration: 8}, "sandbox_generation_stale", 409, `"current_generation":8`},
		{&store.SandboxResetRequiredError{CurrentProvider: "docker", RequestedProvider: "e2b"}, "sandbox_reset_required", 409, `"requested_provider":"e2b"`},
		{&store.SandboxResetRequiredError{CurrentProvider: "e2b", RequestedProvider: "e2b"}, "sandbox_reset_required", 409, `"current_provider":"e2b"`},
		{&store.SandboxInUseError{Resources: store.SandboxDeploymentResources{Allocations: 2, Pending: 1}}, "sandbox_in_use", 409, `"allocations":2`},
		{store.ErrSandboxResetInProgress, "sandbox_reset_in_progress", 409, ""},
		{store.ErrSandboxResetAdmission, "sandbox_reset_in_progress", 503, ""},
	} {
		for _, core := range []bool{false, true} {
			handler := http.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { writeStoreError(w, r, tc.err) }))
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
