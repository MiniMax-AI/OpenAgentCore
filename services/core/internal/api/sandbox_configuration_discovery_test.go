package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

func TestSandboxE2BDiscoveryAuthenticationAndCredentialPrivacy(t *testing.T) {
	deps, fakes := sandboxFakes(t)
	calls := 0
	discover := func(ctx context.Context, kind string, input sandbox.ConfigurationDiscoveryInput) (json.RawMessage, error) {
		calls++
		if kind != "e2b" || !strings.Contains(string(input.Credential), "private-test-key") {
			t.Fatal("wrong request")
		}
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("unbounded discovery")
		}
		if strings.Contains(string(input.Query), "bad") {
			return nil, sandbox.ErrConfigurationUnconfirmed
		}
		if strings.Contains(string(input.Query), "tpl_123") {
			return json.RawMessage(`{"builds":[]}`), nil
		}
		return json.RawMessage(`{"templates":[]}`), nil
	}
	fakes.configurationDiscovery.discoverConfiguration = discover
	h := newTestHandler(t, deps)
	for _, tc := range []struct {
		token, path, body string
		status            int
	}{
		{"caller", "/core/v1/sandbox/providers/e2b/discovery", `{"credential":{"api_key":"private-test-key"},"query":{}}`, 401},
		{"administrator", "/core/v1/sandbox/providers/e2b/discovery", `{"credential":{"api_key":"private-test-key"},"unexpected":true}`, 400},
		{"administrator", "/core/v1/sandbox/providers/e2b/discovery", `{"credential":{"api_key":"private-test-key"},"query":{}}`, 200},
		{"administrator", "/core/v1/sandbox/providers/e2b/discovery", `{"credential":{"api_key":"private-test-key"},"query":{"template":"tpl_123"}}`, 200},
		{"administrator", "/core/v1/sandbox/providers/e2b/discovery", `{"credential":{"api_key":"private-test-key"},"query":{"template":"bad"}}`, 503},
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
