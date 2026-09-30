package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
	"github.com/google/uuid"
)

const savedProviderFixture = `{"protocol":"responses","base_url":"https://example.test/v1","api_key":"saved-provider-secret","context_window":100000,"max_output_tokens":8000}`

type savedProviderStore struct {
	ResourceStore
	saved    store.SavedAgent
	provider *v1.ModelProviderInput
}

func (s *savedProviderStore) CreateAgent(_ context.Context, tenant string, input store.CreateAgentInput) (store.SavedAgent, error) {
	s.provider = input.ModelProvider
	s.saved = store.SavedAgent{ID: uuid.NewString(), TenantID: tenant, Configuration: input.Configuration, Metadata: input.Metadata}
	return s.saved, nil
}

func (s *savedProviderStore) UpdateAgent(_ context.Context, _, _ string, input store.UpdateAgentInput) (store.SavedAgent, error) {
	s.provider = input.ModelProvider
	s.saved.Configuration = input.Configuration
	return s.saved, nil
}

func (s *savedProviderStore) GetAgent(context.Context, string, string) (store.SavedAgent, error) {
	return s.saved, nil
}

func (s *savedProviderStore) ListAgents(context.Context, string, string, int, bool) (store.AgentPage, error) {
	return store.AgentPage{Agents: []store.SavedAgent{s.saved}}, nil
}

func TestSavedProviderReadRedaction(t *testing.T) {
	h, recording, _ := testHandler(t)
	s := &savedProviderStore{}
	recording.ResourceStore = s
	body := `{"model":"fixture","x_agents_core":{"harness":"codex","model_provider":` + savedProviderFixture + `}}`
	created := credentialRequest(h, http.MethodPost, "/v1/agents", body)
	if created.Code != http.StatusCreated || s.provider == nil || s.provider.APIKey != "saved-provider-secret" {
		t.Fatalf("create failed: %d %s", created.Code, created.Body)
	}
	for _, tc := range []struct {
		method, path, body string
		status             int
	}{
		{http.MethodGet, "/v1/agents/" + s.saved.ID, "", http.StatusOK},
		{http.MethodGet, "/v1/agents", "", http.StatusOK},
		{http.MethodPost, "/v1/agents/" + s.saved.ID, body, http.StatusOK},
	} {
		response := credentialRequest(h, tc.method, tc.path, tc.body)
		if response.Code != tc.status {
			t.Fatalf("%s: %d %s", tc.path, response.Code, response.Body)
		}
		assertSavedProviderRedacted(t, response.Body.String())
	}
	assertSavedProviderRedacted(t, created.Body.String())
	assertSavedProviderRedacted(t, string(s.saved.Configuration))
}

func assertSavedProviderRedacted(t *testing.T, raw string) {
	t.Helper()
	if strings.Contains(raw, "saved-provider-secret") || strings.Contains(raw, `"api_key":`) || !strings.Contains(raw, `"api_key_configured":true`) || !strings.Contains(raw, `"base_url":"https://example.test/v1"`) {
		t.Fatalf("incorrect safe provider view: %s", raw)
	}
}

func TestSavedProviderWithoutHarnessDefersCompatibility(t *testing.T) {
	h, recording, _ := testHandler(t)
	s := &savedProviderStore{}
	recording.ResourceStore = s
	provider := strings.Replace(savedProviderFixture, `"responses"`, `"anthropic"`, 1)
	response := credentialRequest(h, http.MethodPost, "/v1/agents", `{"model":"fixture","x_agents_core":{"model_provider":`+provider+`}}`)
	if response.Code != http.StatusCreated || s.provider == nil || s.provider.Protocol != "anthropic" {
		t.Fatalf("deployment default must not constrain saved provider: %d %s", response.Code, response.Body)
	}
	if strings.Contains(response.Body.String(), `"harness"`) {
		t.Fatal("saving a provider must not infer a harness")
	}
}

func TestSavedProviderUpdatePresence(t *testing.T) {
	for _, tc := range []struct {
		name, body, patch string
		set, provider     bool
	}{
		{"omitted extension", `{"model":"new"}`, `{"model":"new"}`, false, false},
		{"harness only", `{"x_agents_core":{"harness":"codex"}}`, `{"x_agents_core":{"harness":"codex"}}`, false, false},
		{"empty extension", `{"x_agents_core":{}}`, `{"x_agents_core":{}}`, false, false},
		{"clear extension", `{"x_agents_core":null}`, `{"x_agents_core":null}`, true, false},
		{"clear provider", `{"x_agents_core":{"model_provider":null}}`, `{"x_agents_core":{"model_provider":null}}`, true, false},
		{"replace provider", `{"x_agents_core":{"model_provider":` + savedProviderFixture + `}}`, "", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input, err := resolveAgentUpdate([]byte(tc.body))
			if err != nil || input.ModelProviderSet != tc.set || (input.ModelProvider != nil) != tc.provider {
				t.Fatalf("set=%v provider=%v err=%v", input.ModelProviderSet, input.ModelProvider != nil, err)
			}
			if tc.patch != "" && string(input.Configuration) != tc.patch {
				t.Fatalf("patch=%s", input.Configuration)
			}
			if tc.provider {
				assertSavedProviderRedacted(t, string(input.Configuration))
				var cfg v1.SavedAgentConfiguration
				if json.Unmarshal(input.Configuration, &cfg) != nil || cfg.XAgentsCore.Harness != "" {
					t.Fatal("provider-only patch must preserve the stored harness")
				}
			}
		})
	}
}

func TestSavedProviderInvalidInput(t *testing.T) {
	for _, extension := range []string{
		`{"harness":""}`, `{"harness":null}`, `{"harness":"saved-provider-secret"}`,
		`{"model_provider":{}}`, `{"model_provider":{"protocol":"responses"}}`,
		`{"model_provider":{"protocol":"responses","base_url":"https://example.test","api_key_configured":true}}`,
		`{"model_provider":{"protocol":"responses","base_url":"https://example.test","api_key":"saved-provider-secret","api_key_configured":true}}`,
		`{"model_provider":{"protocol":"responses","base_url":"https://example.test","api_key":"saved-provider-secret","context_window":null}}`,
		`{"model_provider":{"protocol":"responses","base_url":"https://example.test","api_key":null}}`,
		`{"model_provider":{"protocol":"responses","base_url":"http://example.test","api_key":"saved-provider-secret"}}`,
		`{"model_provider":{"protocol":"responses","base_url":"https://user:saved-provider-secret@example.test","api_key":"saved-provider-secret"}}`,
		`{"model_provider":{"protocol":"responses","base_url":"https://example.test","api_key":"saved-provider-secret","API_KEY":"secret"}}`,
		`{"model_provider":{"protocol":"responses","base_url":"https://example.test","api_key":"saved-provider-secret","api_key":"other"}}`,
		`{"harness":"claude_sdk","model_provider":` + strings.Replace(savedProviderFixture, `"responses"`, `"unknown"`, 1) + `}`,
		`{"model_provider":` + strings.Replace(savedProviderFixture, `"max_output_tokens":8000`, `"max_output_tokens":100001`, 1) + `}`,
	} {
		for _, path := range []string{"/v1/agents", "/v1/agents/" + uuid.NewString()} {
			h, s := validationHandler(t)
			response := credentialRequest(h, http.MethodPost, path, `{"model":"fixture","x_agents_core":`+extension+`}`)
			if response.Code != http.StatusBadRequest || s.writes != 0 || strings.Contains(response.Body.String(), "saved-provider-secret") {
				t.Fatalf("extension=%s status=%d writes=%d body=%s", extension, response.Code, s.writes, response.Body)
			}
		}
	}
}

func TestSavedProviderProtocolHarnessMatrix(t *testing.T) {
	for _, harness := range []string{"codex", "claude_sdk", "mcode"} {
		for _, protocol := range []string{"anthropic", "responses", "chat_completions"} {
			t.Run(harness+"/"+protocol, func(t *testing.T) {
				h, recording, _ := testHandler(t)
				s := &savedProviderStore{}
				recording.ResourceStore = s
				provider := strings.Replace(savedProviderFixture, `"responses"`, `"`+protocol+`"`, 1)
				body := `{"model":"fixture","x_agents_core":{"harness":"` + harness + `","model_provider":` + provider + `}}`
				for _, path := range []string{"/v1/agents", "/v1/agents/" + uuid.NewString()} {
					response := credentialRequest(h, http.MethodPost, path, body)
					native := harness == "mcode" || harness == "codex" && protocol == "responses" || harness == "claude_sdk" && protocol == "anthropic"
					if !native {
						if response.Code != http.StatusBadRequest || s.provider != nil || !strings.Contains(response.Body.String(), "does not support this model provider protocol") || strings.Contains(response.Body.String(), "saved-provider-secret") {
							t.Fatalf("non-native provider reached storage or returned an unsafe rejection: status=%d", response.Code)
						}
						continue
					}
					want := http.StatusOK
					if path == "/v1/agents" {
						want = http.StatusCreated
					}
					if response.Code != want || s.provider == nil || s.provider.Protocol != protocol || s.provider.APIKey != "saved-provider-secret" {
						t.Fatalf("provider bundle rejected or changed: status=%d", response.Code)
					}
					assertSavedProviderRedacted(t, response.Body.String())
					var result struct {
						Core v1.SavedAgentCore `json:"x_agents_core"`
					}
					if json.Unmarshal(response.Body.Bytes(), &result) != nil || result.Core.Harness != harness || result.Core.ModelProvider == nil || result.Core.ModelProvider.Protocol != protocol {
						t.Fatal("safe view changed the selected harness or upstream protocol")
					}
				}
			})
		}
	}
}
