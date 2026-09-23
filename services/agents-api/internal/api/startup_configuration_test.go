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
		{name: "query", option: []Option{WithStartupConfiguration(configured)}, path: "/v1/agents/core/startup-configuration?raw=true", auth: "Bearer test-api-key", status: http.StatusBadRequest},
		{name: "malformed query", option: []Option{WithStartupConfiguration(configured)}, path: "/v1/agents/core/startup-configuration?raw;private", auth: "Bearer test-api-key", status: http.StatusBadRequest},
		{name: "unknown include", option: []Option{WithStartupConfiguration(configured)}, path: "/v1/agents/core/startup-configuration?include=private", auth: "Bearer test-api-key", status: http.StatusBadRequest},
		{name: "empty include", option: []Option{WithStartupConfiguration(configured)}, path: "/v1/agents/core/startup-configuration?include=", auth: "Bearer test-api-key", status: http.StatusBadRequest},
		{name: "duplicate include", option: []Option{WithStartupConfiguration(configured)}, path: "/v1/agents/core/startup-configuration?include=configuration_capabilities&include=configuration_capabilities", auth: "Bearer test-api-key", status: http.StatusBadRequest},
		{name: "additional key", option: []Option{WithStartupConfiguration(configured)}, path: "/v1/agents/core/startup-configuration?include=configuration_capabilities&raw=true", auth: "Bearer test-api-key", status: http.StatusBadRequest},
		{name: "malformed encoding", option: []Option{WithStartupConfiguration(configured)}, path: "/v1/agents/core/startup-configuration?include=%GG", auth: "Bearer test-api-key", status: http.StatusBadRequest},
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

func TestConfigurationCapabilitiesHTTPIsStableAndDefensive(t *testing.T) {
	capabilities := &v1.CoreConfigurationCapabilities{
		SchemaVersion: 1, Scope: "core_build_provider_configuration", RuntimeAvailability: "unknown",
		Admission: v1.ModelProviderConfigurationAdmission(),
		Harnesses: []v1.CoreHarnessConfigurationSupport{{
			Harness: "additional-adapter", Support: "supported", Enabled: true, Default: true,
			Providers: []v1.CoreProviderProtocolSupport{{Protocol: "fixture", RequiredFields: []string{"protocol", "base_url", "api_key", "context_window", "max_output_tokens"}, PositiveFields: []string{"context_window", "max_output_tokens"}}},
		}},
	}
	configuration := v1.CoreStartupConfiguration{Object: "agents.core.startup_configuration", SchemaVersion: 1, ConfigurationCapabilities: capabilities}
	h, _, _ := testHandler(t, WithStartupConfiguration(configuration))
	capabilities.Admission.CredentialEnvironmentTypes[0] = "private-canary"
	capabilities.Admission.BaseURL.Schemes[0] = "private-canary"
	capabilities.Harnesses[0].Harness = "private-canary"
	capabilities.Harnesses[0].Providers[0].Protocol = "private-canary"
	capabilities.Harnesses[0].Providers[0].RequiredFields[0] = "private-canary"
	capabilities.Harnesses[0].Providers[0].PositiveFields[0] = "private-canary"

	previous := ""
	for i := 0; i < 2; i++ {
		request := httptest.NewRequest(http.MethodGet, "/v1/agents/core/startup-configuration?include=configuration_capabilities", nil)
		request.Header.Set("Authorization", "Bearer test-api-key")
		request.Header.Set("OpenAI-Beta", "agents=v1")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, request)
		if w.Code != http.StatusOK || strings.Contains(w.Body.String(), "private-canary") {
			t.Fatalf("unsafe response: %d %s", w.Code, w.Body)
		}
		if i > 0 && w.Body.String() != previous {
			t.Fatal("startup response changed between reads")
		}
		previous = w.Body.String()
		var got v1.CoreStartupConfiguration
		if json.Unmarshal(w.Body.Bytes(), &got) != nil || got.ConfigurationCapabilities == nil {
			t.Fatal("missing capability extension")
		}
		entry := got.ConfigurationCapabilities.Harnesses[0]
		if entry.Harness != "additional-adapter" || entry.Providers[0].RequiredFields[0] != "protocol" || entry.Providers[0].PositiveFields[0] != "context_window" {
			t.Fatal("shared snapshot was mutated")
		}
		if got.ConfigurationCapabilities.RuntimeAvailability != "unknown" {
			t.Fatal("startup view advertised Runtime readiness")
		}
	}
	request := httptest.NewRequest(http.MethodGet, "/v1/agents/core/startup-configuration", nil)
	request.Header.Set("Authorization", "Bearer test-api-key")
	request.Header.Set("OpenAI-Beta", "agents=v1")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, request)
	var legacy map[string]json.RawMessage
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &legacy) != nil || len(legacy) != 4 || legacy["configuration_capabilities"] != nil {
		t.Fatalf("default read broke the legacy startup shape: %d %s", w.Code, w.Body)
	}
	for _, auth := range []string{"", "Bearer wrong-private-canary"} {
		request := httptest.NewRequest(http.MethodGet, "/v1/agents/core/startup-configuration?api_key=private-canary", nil)
		request.Header.Set("Authorization", auth)
		request.Header.Set("OpenAI-Beta", "agents=v1")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, request)
		if w.Code != http.StatusUnauthorized || strings.Contains(w.Body.String(), "private-canary") || strings.Contains(w.Body.String(), "additional-adapter") {
			t.Fatalf("unauthorized configuration exposure: %d %s", w.Code, w.Body)
		}
	}
}
