package modelprovider

import (
	"errors"
	"testing"
)

func TestParseProviderValidatesFrozenBundle(t *testing.T) {
	valid := map[string]any{"protocol": "responses", "base_url": "https://model.example/api", "api_key": "fixture-upstream-key", "context_window": int32(64000), "max_output_tokens": int32(4096)}
	for _, protocol := range []Protocol{Anthropic, Responses, ChatCompletions} {
		t.Run(string(protocol), func(t *testing.T) {
			raw := copyBundle(valid)
			raw["protocol"] = string(protocol)
			got, err := ParseProvider(raw)
			want := Provider{Protocol: protocol, BaseURL: "https://model.example/api", APIKey: "fixture-upstream-key", ContextWindow: 64000, MaxOutputTokens: 4096}
			if err != nil || got != want {
				t.Fatalf("bundle changed or rejected: %v", err)
			}
		})
	}
	for _, tc := range []struct {
		name, field string
		value       any
	}{
		{"unknown field", "native_options", map[string]any{}}, {"alias", "protocol", "openai"},
		{"missing key", "api_key", ""}, {"newline key", "api_key", "fixture\nkey"},
		{"URL credentials", "base_url", "https://user:secret@model.example/v1"},
		{"remote HTTP", "base_url", "http://model.example/v1"},
		{"query", "base_url", "https://model.example/v1?key=secret"},
		{"fragment", "base_url", "https://model.example/v1#secret"},
		{"negative limit", "context_window", -1}, {"excess output", "max_output_tokens", 64001},
		{"fractional limit", "context_window", 1.5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := copyBundle(valid)
			raw[tc.field] = tc.value
			if _, err := ParseProvider(raw); !errors.Is(err, ErrConfiguration) {
				t.Fatalf("invalid bundle accepted: %v", err)
			}
		})
	}
	for _, raw := range []any{nil, []any{}, "provider", map[string]any{}} {
		if _, err := ParseProvider(raw); !errors.Is(err, ErrConfiguration) {
			t.Fatalf("invalid root accepted: %v", err)
		}
	}
}

func TestAnthropicBaseURLExcludesVersionPath(t *testing.T) {
	for _, tc := range []struct {
		protocol Protocol
		baseURL  string
		valid    bool
	}{
		{Anthropic, "https://model.example", true},
		{Anthropic, "https://model.example/", true},
		{Anthropic, "https://model.example/anthropic", true},
		{Anthropic, "https://model.example/v1beta", true},
		{Anthropic, "https://model.example/v1", false},
		{Anthropic, "https://model.example/v1/", false},
		{Anthropic, "https://model.example/anthropic/v1//", false},
		{Responses, "https://model.example/v1", true},
		{ChatCompletions, "https://model.example/v1/", true},
	} {
		err := Provider{Protocol: tc.protocol, BaseURL: tc.baseURL, APIKey: "fixture-upstream-key"}.Validate()
		if tc.valid != (err == nil) || err != nil && !errors.Is(err, ErrConfiguration) {
			t.Fatalf("%s %s: %v", tc.protocol, tc.baseURL, err)
		}
	}
}

func copyBundle(raw map[string]any) map[string]any {
	out := make(map[string]any, len(raw))
	for k, v := range raw {
		out[k] = v
	}
	return out
}
