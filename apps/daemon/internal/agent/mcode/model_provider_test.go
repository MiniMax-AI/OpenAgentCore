package mcode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/modelprovider"
)

func TestOptionsModelProviderProtocols(t *testing.T) {
	for _, tc := range []struct{ protocol, api string }{
		{"anthropic", "anthropic-messages"}, {"responses", "openai-responses"}, {"chat_completions", "openai-completions"},
	} {
		t.Run(tc.protocol, func(t *testing.T) {
			req := testRequest(t)
			req.ModelProvider.Protocol = modelprovider.Protocol(tc.protocol)
			req.Model = "chosen-model"
			opts, err := prepareOptions(prepared(t, req))
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
				"options": map[string]any{"baseURL": "https://provider.example", "apiKey": "fixture-key"},
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
