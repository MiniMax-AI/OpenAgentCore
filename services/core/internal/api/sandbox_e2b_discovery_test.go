package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/device"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/e2b"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
)

func TestSandboxE2BDiscoveryAuthenticationAndCredentialPrivacy(t *testing.T) {
	project, _ := NewAuthenticator([]APIKey{callerBinding()})
	admin, _ := NewDeploymentAuthenticator([]string{device.HashCredential("administrator")})
	calls := 0
	discover := func(_ context.Context, input SandboxE2BDiscoveryInput, template string) (SandboxE2BDiscoveryResult, error) {
		calls++
		if input.APIKey != "private-test-key" {
			t.Fatal("wrong credential")
		}
		if template == "bad" {
			return SandboxE2BDiscoveryResult{}, errors.New("private-test-key in provider error")
		}
		if template != "" {
			return SandboxE2BDiscoveryResult{Builds: []e2b.ReadyBuild{}}, nil
		}
		return SandboxE2BDiscoveryResult{Templates: []e2b.TemplateSummary{}}, nil
	}
	h, err := NewHandler(&recordingStore{}, project, "codex", WithSandboxManager(&store.Store{}, admin), WithSandboxE2BDiscovery(discover))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		token, path, body string
		status            int
	}{
		{"caller", "/core/v1/sandbox/e2b/templates", `{"api_key":"private-test-key"}`, 401},
		{"administrator", "/core/v1/sandbox/e2b/templates", `{"api_key":"private-test-key","unexpected":true}`, 400},
		{"administrator", "/core/v1/sandbox/e2b/templates", `{"api_key":"private-test-key"}`, 200},
		{"administrator", "/core/v1/sandbox/e2b/templates/tpl_123/builds", `{"api_key":"private-test-key"}`, 200},
		{"administrator", "/core/v1/sandbox/e2b/templates/bad/builds", `{"api_key":"private-test-key"}`, 503},
	} {
		req := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body))
		req.Header.Set("Authorization", "Bearer "+tc.token)
		result := httptest.NewRecorder()
		h.ServeHTTP(result, req)
		if result.Code != tc.status || strings.Contains(result.Body.String(), "private-test-key") {
			t.Fatal(result.Code, result.Body.String())
		}
	}
	if calls != 3 {
		t.Fatal(calls)
	}
}
