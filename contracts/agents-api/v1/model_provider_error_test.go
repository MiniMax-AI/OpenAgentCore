package v1

import (
	"errors"
	"testing"
)

func TestModelProviderErrorMessagesAndPrecedence(t *testing.T) {
	valid := ModelProviderInput{Protocol: "responses", BaseURL: "https://example.test", APIKey: "private-key"}
	for _, tc := range []struct {
		name, harness, code, param, message string
		change                              func(*ModelProviderInput)
	}{
		{"url first", "codex", "model_provider_base_url_invalid", "base_url", "model provider requires an http or https base_url without credentials, query or fragment", func(p *ModelProviderInput) {
			p.BaseURL = "ftp://private.example"
			p.Protocol = "private"
			p.APIKey = ""
		}},
		{"protocol before key", "codex", "model_provider_protocol_unsupported", "protocol", "unsupported model provider protocol", func(p *ModelProviderInput) { p.Protocol = "private"; p.APIKey = "" }},
		{"key", "claude_sdk", "model_provider_api_key_invalid", "api_key", "invalid model provider API key", func(p *ModelProviderInput) { p.Protocol = "anthropic"; p.APIKey = "" }},
		{"context before output", "codex", "model_provider_token_limits_invalid", "context_window", "invalid model token limits", func(p *ModelProviderInput) { p.ContextWindow = -1; p.MaxOutputTokens = -2 }},
		{"output", "codex", "model_provider_token_limits_invalid", "max_output_tokens", "invalid model token limits", func(p *ModelProviderInput) { p.MaxOutputTokens = 1 }},
		{"required context", "mcode", "model_provider_token_limits_invalid", "context_window", "selected harness requires positive model context_window and max_output_tokens", func(p *ModelProviderInput) { p.Protocol = "anthropic" }},
		{"required output", "mcode", "model_provider_token_limits_invalid", "max_output_tokens", "selected harness requires positive model context_window and max_output_tokens", func(p *ModelProviderInput) { p.Protocol = "anthropic"; p.ContextWindow = 10 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := valid
			tc.change(&p)
			err := p.ValidateHarness(tc.harness)
			var field *ModelProviderError
			if !errors.As(err, &field) || field.Code != tc.code || field.Param != tc.param || err.Error() != tc.message {
				t.Fatalf("wrong typed error: %#v", err)
			}
		})
	}
	var missing *ModelProviderInput
	var field *ModelProviderError
	if err := missing.Validate(); !errors.As(err, &field) || field.Code != "invalid_model_provider" || field.Param != "" || err.Error() != "model_provider is required" {
		t.Fatal(err)
	}
	if err := valid.ValidateHarness("codex"); err != nil {
		t.Fatal(err)
	}
}
