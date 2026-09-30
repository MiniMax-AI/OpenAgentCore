package mcode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestOptionsModelProviderProtocols(t *testing.T) {
	for _, tc := range []struct{ protocol, api string }{
		{"anthropic", "anthropic-messages"}, {"responses", "openai-responses"}, {"chat_completions", "openai-completions"},
	} {
		t.Run(tc.protocol, func(t *testing.T) {
			req := testRequest(t)
			req.AgentOptions["model_provider"].(map[string]any)["protocol"] = tc.protocol
			req.AgentOptions["model"] = "chosen-model"
			opts, err := prepareOptions(t.Context(), req)
			if err != nil {
				t.Fatal(err)
			}
			body, err := os.ReadFile(filepath.Join(opts.DataDir, "config.yaml"))
			if err != nil {
				t.Fatal(err)
			}
			var config map[string]any
			if err := json.Unmarshal(body, &config); err != nil {
				t.Fatal(err)
			}
			want := map[string]any{"oac": map[string]any{
				"name": "Configured provider", "kind": "custom", "enabled": true, "api": tc.api,
				"options": map[string]any{"baseURL": "https://provider.example/v1", "apiKey": "fixture-key"},
				"models": map[string]any{"chosen-model": map[string]any{
					"name": "chosen-model", "tool_call": true,
					"limit": map[string]any{"context": float64(64000), "output": float64(4096)},
				}},
			}}
			if opts.Model != "chosen-model" || !reflect.DeepEqual(config["custom_provider"], want) {
				t.Fatal("native provider configuration did not preserve the upstream bundle and selected model")
			}
		})
	}
}

func TestOptionsRejectInvalidProvider(t *testing.T) {
	for _, tc := range []struct {
		name, field string
		value       any
	}{
		{"unknown protocol", "protocol", "unknown"},
		{"protocol alias", "protocol", "chat-completions"},
		{"remote HTTP", "base_url", "http://provider.example/v1"},
		{"empty key", "api_key", ""},
		{"missing context", "context_window", 0},
		{"missing output", "max_output_tokens", 0},
		{"excess output", "max_output_tokens", 64001},
		{"native options", "options", map[string]any{"apiKey": "fixture-key"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := testRequest(t)
			req.AgentOptions["model_provider"].(map[string]any)[tc.field] = tc.value
			if _, err := prepareOptions(t.Context(), req); err == nil {
				t.Fatal("invalid model provider accepted")
			}
		})
	}
	t.Run("retired native option", func(t *testing.T) {
		req := testRequest(t)
		req.AgentOptions["mcode_provider"] = req.AgentOptions["model_provider"]
		delete(req.AgentOptions, "model_provider")
		if _, err := prepareOptions(t.Context(), req); err == nil {
			t.Fatal("retired native provider option accepted")
		}
	})
}

func TestOptionsAllowLoopbackProviderFixture(t *testing.T) {
	req := testRequest(t)
	req.AgentOptions["model_provider"].(map[string]any)["base_url"] = "http://127.0.0.1:4321/v1"
	if _, err := prepareOptions(t.Context(), req); err != nil {
		t.Fatal(err)
	}
}
