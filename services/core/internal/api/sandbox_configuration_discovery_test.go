package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

func TestSandboxE2BDiscoveryAuthenticationAndCredentialPrivacy(t *testing.T) {
	deps, fakes := sandboxFakes(t)
	calls := 0
	discover := func(ctx context.Context, kind string, input sandbox.ConfigurationDiscoveryInput) (sandbox.ConfigurationOptions, error) {
		calls++
		if kind != "e2b" || !strings.Contains(string(input.Credential), "private-test-key") {
			t.Fatal("wrong request")
		}
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("unbounded discovery")
		}
		if len(input.Query.Path) == 1 && input.Query.Path[0] == "bad" {
			return sandbox.ConfigurationOptions{}, sandbox.ErrConfigurationUnconfirmed
		}
		return sandbox.ConfigurationOptions{Options: []sandbox.ConfigurationOption{}}, nil
	}
	fakes.configurationDiscovery.discoverConfiguration = discover
	h := newTestHandler(t, deps)
	for _, tc := range []struct {
		token, path, body string
		status            int
	}{
		{"caller", "/core/v1/sandbox/providers/e2b/discovery", `{"credential":{"api_key":"private-test-key"},"query":{"field":"template","path":[]}}`, 401},
		{"administrator", "/core/v1/sandbox/providers/e2b/discovery", `{"credential":{"api_key":"private-test-key"},"unexpected":true}`, 400},
		{"administrator", "/core/v1/sandbox/providers/e2b/discovery", `{"credential":{"api_key":"private-test-key"},"query":{"field":"template","path":[]}}`, 200},
		{"administrator", "/core/v1/sandbox/providers/e2b/discovery", `{"credential":{"api_key":"private-test-key"},"query":{"field":"template","path":["tpl_123"]}}`, 200},
		{"administrator", "/core/v1/sandbox/providers/e2b/discovery", `{"credential":{"api_key":"private-test-key"},"query":{"field":"template","path":["bad"]}}`, 503},
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
