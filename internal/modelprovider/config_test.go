package modelprovider

import (
	"errors"
	"testing"
)

func TestParseProviderValidatesFrozenBundle(t *testing.T) {
	valid := map[string]any{"protocol": "responses", "base_url": "https://model.example/v1", "api_key": "fixture-upstream-key", "context_window": int32(64000), "max_output_tokens": int32(4096)}
	for _, protocol := range []Protocol{Anthropic, Responses, ChatCompletions} {
		t.Run(string(protocol), func(t *testing.T) {
			raw := copyBundle(valid)
			raw["protocol"] = string(protocol)
			got, err := ParseProvider(raw)
			want := Provider{Protocol: protocol, BaseURL: "https://model.example/v1", APIKey: "fixture-upstream-key", ContextWindow: 64000, MaxOutputTokens: 4096}
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
		{"unsupported scheme", "base_url", "ftp://model.example/v1"},
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

func copyBundle(raw map[string]any) map[string]any {
	out := make(map[string]any, len(raw))
	for k, v := range raw {
		out[k] = v
	}
	return out
}
