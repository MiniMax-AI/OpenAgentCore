package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

func TestExecutionConfigurationSources(t *testing.T) {
	provider := &v1.ModelProviderInput{Protocol: "responses", BaseURL: "https://saved.example/v1", APIKey: "secret-canary"}
	saved := &v1.SavedAgent{SavedAgentConfiguration: v1.SavedAgentConfiguration{Model: "saved-model", XAgentsCore: &v1.SavedAgentCore{Harness: "codex", ModelProvider: provider.SafeView()}}}
	for _, tc := range []struct {
		name, agent, extension                             string
		saved                                              *v1.SavedAgent
		inherited, provider                                *v1.ModelProviderInput
		modelSource, harnessSource, providerSource, status string
	}{
		{"saved", ``, ``, saved, provider, provider, "agent", "agent", "agent", "available"},
		{"model override", `,"agent":{"model":"override"}`, ``, saved, provider, provider, "session", "agent", "agent", "available"},
		{"harness override", `,"agent":{"x_agents_core":{"harness":"codex"}}`, ``, saved, provider, provider, "agent", "session", "agent", "available"},
		{"harness reset", `,"agent":{"x_agents_core":null}`, ``, saved, provider, provider, "agent", "deployment", "agent", "available"},
		{"explicit bundle", ``, `,"x_agents_core":{"model_provider":{"protocol":"responses","base_url":"https://explicit.example/v1","api_key":"explicit-secret"}}`, saved, nil, provider, "agent", "agent", "session", "available"},
		{"provider null inherits", ``, `,"x_agents_core":{"model_provider":null}`, saved, provider, provider, "agent", "agent", "agent", "available"},
		{"inline deployment", `,"agent":{"model":"inline"}`, ``, nil, nil, provider, "session", "deployment", "deployment", "available"},
		{"inline explicit harness", `,"agent":{"model":"inline","x_agents_core":{"harness":"codex"}}`, ``, nil, nil, nil, "session", "session", "unknown", "unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var decoded decodedSessionRequest
			if err := json.Unmarshal([]byte(`{"environment":{"type":"openai_hosted"}`+tc.agent+tc.extension+`}`), &decoded); err != nil {
				t.Fatal(err)
			}
			input, err := decoded.validated()
			if err != nil {
				t.Fatal(err)
			}
			p := sessionExecutionProjection(input, tc.saved, tc.inherited, tc.provider, "codex", json.RawMessage(`{"agent":{"model":"resolved"}}`))
			if p.Model.Source != tc.modelSource || p.Harness.Source != tc.harnessSource || p.ModelProvider.Source != tc.providerSource || p.ModelProvider.Status != tc.status {
				t.Fatalf("wrong sources: %#v", p)
			}
			raw, _ := json.Marshal(p)
			if strings.Contains(string(raw), "secret-canary") || (tc.status != "available" && p.ModelProvider.Configuration != nil) {
				t.Fatal("private provider leaked")
			}
		})
	}
}

type executionConfigurationReader struct {
	ResourceStore
	calls      int
	tenant, id string
	value      v1.SessionExecutionConfiguration
	err        error
}

func (s *executionConfigurationReader) GetSessionExecutionConfiguration(_ context.Context, tenant, id string) (v1.SessionExecutionConfiguration, error) {
	s.calls++
	s.tenant, s.id = tenant, id
	return s.value, s.err
}

func TestExecutionConfigurationReadBoundary(t *testing.T) {
	s := &executionConfigurationReader{value: v1.SessionExecutionConfiguration{Object: "agent.session.execution_configuration", SchemaVersion: 1, SessionID: "frozen"}}
	h, _, tenant := adminTestHandler(t, func(h *Handler) { h.store = s })
	for _, tc := range []struct {
		auth   string
		err    error
		status int
	}{
		{"", nil, 401}, {"Bearer admin", nil, 200}, {"Bearer admin", store.ErrNotFound, 404},
	} {
		s.err = tc.err
		before := s.calls
		r := httptest.NewRequest(http.MethodGet, adminSessionsPath+"frozen/execution-configuration?ignored=true", nil)
		r.Header.Set("Authorization", tc.auth)
		r.Header.Set("OpenAI-Beta", "agents=v1")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("status %d: %s", w.Code, w.Body)
		}
		if tc.auth == "" {
			if s.calls != before {
				t.Fatal("unauthorized read reached storage")
			}
			continue
		}
		if s.tenant != tenant || s.id != "frozen" {
			t.Fatal("wrong tenant/resource")
		}
		if w.Code == 200 && w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("snapshot response cacheable")
		}
	}
}
