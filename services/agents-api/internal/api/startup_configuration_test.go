package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
)

func TestStartupConfigurationHTTP(t *testing.T) {
	provider := "docker"
	configuration := v1.CoreStartupConfiguration{
		Object: "agents.core.startup_configuration", SchemaVersion: 1,
		Supported: v1.CoreSupportedConfiguration{Harnesses: []string{"claude_sdk", "codex", "mcode"}, ManagedSandboxProviders: []string{"docker", "microsandbox"}},
		Configured: v1.CoreConfiguredStartupConfiguration{
			DefaultHarness: "codex", EnabledHarnesses: []string{"claude_sdk", "codex"}, DaemonGateway: true, SelfHosted: true,
			ManagedSandbox: v1.CoreManagedSandboxConfiguration{Enabled: true, Provider: &provider},
			ModelProviders: []v1.CoreHarnessModelProviderConfiguration{{Harness: "claude_sdk"}, {Harness: "codex", EndpointConfigured: true}},
		},
	}
	h, _, _ := testHandler(t, WithStartupConfiguration(configuration))
	configuration.Supported.Harnesses[0] = "mutated"
	configuration.Configured.EnabledHarnesses[0] = "mutated"
	*configuration.Configured.ManagedSandbox.Provider = "mutated"

	request := httptest.NewRequest(http.MethodGet, "/v1/agents/core/startup-configuration", nil)
	request.Header.Set("Authorization", "Bearer test-api-key")
	request.Header.Set("OpenAI-Beta", "agents=v1")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, request)
	var response v1.CoreStartupConfiguration
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &response) != nil {
		t.Fatalf("response = %d %s", w.Code, w.Body)
	}
	if response.Object != "agents.core.startup_configuration" || response.SchemaVersion != 1 || response.Configured.DefaultHarness != "codex" || response.Configured.ManagedSandbox.Provider == nil || *response.Configured.ManagedSandbox.Provider != "docker" {
		t.Fatalf("unexpected response: %#v", response)
	}
	if strings.Contains(w.Body.String(), "mutated") || strings.Contains(w.Body.String(), "base_url") || strings.Contains(w.Body.String(), "token") {
		t.Fatalf("mutable or private configuration leaked: %s", w.Body)
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(w.Body.Bytes(), &fields) != nil || len(fields) != 4 || fields["configuration_capabilities"] != nil {
		t.Fatal("startup response includes provider discovery")
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q", w.Header().Get("Cache-Control"))
	}
}

func TestStartupConfigurationRejectsUnsupportedReads(t *testing.T) {
	configured := v1.CoreStartupConfiguration{Object: "agents.core.startup_configuration", SchemaVersion: 1}
	for _, test := range []struct {
		name   string
		option []Option
		path   string
		auth   string
		status int
	}{
		{name: "missing auth", option: []Option{WithStartupConfiguration(configured)}, path: "/v1/agents/core/startup-configuration", status: http.StatusUnauthorized},
		{name: "retired discovery", option: []Option{WithStartupConfiguration(configured)}, path: "/v1/agents/core/startup-configuration?include=configuration_capabilities", auth: "Bearer test-api-key", status: http.StatusBadRequest},
		{name: "query", option: []Option{WithStartupConfiguration(configured)}, path: "/v1/agents/core/startup-configuration?raw=true", auth: "Bearer test-api-key", status: http.StatusBadRequest},
		{name: "malformed query", option: []Option{WithStartupConfiguration(configured)}, path: "/v1/agents/core/startup-configuration?raw;private", auth: "Bearer test-api-key", status: http.StatusBadRequest},
		{name: "composition missing", path: "/v1/agents/core/startup-configuration", auth: "Bearer test-api-key", status: http.StatusServiceUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			h, _, _ := testHandler(t, test.option...)
			request := httptest.NewRequest(http.MethodGet, test.path, nil)
			request.Header.Set("Authorization", test.auth)
			request.Header.Set("OpenAI-Beta", "agents=v1")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, request)
			if w.Code != test.status {
				t.Fatalf("response = %d %s", w.Code, w.Body)
			}
		})
	}
}
