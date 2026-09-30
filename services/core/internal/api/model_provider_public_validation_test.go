package api

import (
	"net/http"
	"testing"
)

func TestModelProviderPublicValidation(t *testing.T) {
	for _, tc := range []struct{ name, provider string }{
		{"url", `{"protocol":"responses","base_url":"http://private-url.example","api_key":"private-key"}`},
		{"protocol", `{"protocol":"private-protocol","base_url":"https://example.test","api_key":"private-key"}`},
		{"key", `{"protocol":"responses","base_url":"https://example.test","api_key":""}`},
		{"limits", `{"protocol":"responses","base_url":"https://example.test","api_key":"private-key","context_window":1,"max_output_tokens":2}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, _, _ := testHandler(t)
			body := `{"agent":{"model":"fixture"},"environment":{"type":"openai_hosted"},"input":"hello","x_agents_core":{"model_provider":` + tc.provider + `}}`
			out := credentialRequest(h, http.MethodPost, "/v1/agents/sessions", body)
			messages := map[string]string{
				"url":      "model provider requires an HTTPS base_url without credentials, query or fragment",
				"protocol": "unsupported model provider protocol", "key": "invalid model provider API key",
				"limits": "invalid model token limits",
			}
			golden := `{"error":{"message":"` + messages[tc.name] + `","type":"invalid_request_error","code":"unsupported_or_invalid_configuration","param":null}}` + "\n"
			if out.Body.String() != golden {
				t.Fatalf("unexpected public validation response: %s", out.Body)
			}
			if out.Code != 400 {
				t.Fatalf("unexpected status: %d %s", out.Code, out.Body)
			}
		})
	}
}
