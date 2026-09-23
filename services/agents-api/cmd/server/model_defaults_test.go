package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

func TestDeploymentProviderBundleForEachHarness(t *testing.T) {
	for _, tc := range []struct {
		harness, raw, protocol string
		context, output        int32
	}{
		{"codex", `{"codex_provider":{"base_url":"https://models.example/v1","bearer_token":"secret-canary","wire_api":" responses ","http_headers":{"X-Provider":"required"},"query_params":{"api-version":"2025-01-01"}}}`, "responses", 0, 0},
		{"claude_sdk", `{"claude_provider":{"base_url":"https://models.example/anthropic","bearer_token":"secret-canary"}}`, "anthropic", 0, 0},
		{"mcode", `{"mcode_provider":{"options":{"baseURL":"https://models.example/anthropic","apiKey":"secret-canary"},"models":{"selected":{"limit":{"context":200000,"output":8000}}}}}`, "anthropic", 200000, 8000},
	} {
		t.Run(tc.harness, func(t *testing.T) {
			resolver := deploymentModelDefaults(func(_ context.Context, s store.Session) (map[string]any, error) {
				if s.Engine != tc.harness {
					t.Fatal("wrong harness")
				}
				var opts map[string]any
				err := json.Unmarshal([]byte(tc.raw), &opts)
				return opts, err
			})
			provider, native, err := resolver(t.Context(), tc.harness, "selected")
			if native == nil || err != nil || provider.Protocol != tc.protocol || provider.APIKey != "secret-canary" || provider.ContextWindow != tc.context || provider.MaxOutputTokens != tc.output {
				t.Fatal("provider conversion failed", err)
			}
			if tc.harness == "codex" {
				providerOptions := native["codex_provider"].(map[string]any)
				if providerOptions["http_headers"] == nil || providerOptions["query_params"] == nil {
					t.Fatal("native deployment options lost")
				}
			}
			provider.APIKey = "changed"
			again, _, err := resolver(t.Context(), tc.harness, "selected")
			if err != nil || again.APIKey != "secret-canary" {
				t.Fatal("mutable defaults shared", err)
			}
		})
	}
}

func TestDeploymentProviderMissingAndInvalidConfiguration(t *testing.T) {
	if deploymentModelDefaults(nil) != nil {
		t.Fatal("invented default")
	}
	for _, tc := range []struct {
		harness, raw string
		valid        bool
	}{
		{"codex", `{}`, true},
		{"codex", `{"codex_provider":{"base_url":"https://user:secret-canary@example.test","bearer_token":"secret-canary"}}`, false},
		{"codex", `{"codex_provider":{"base_url":"https://example.test","bearer_token":"secret-canary","wire_api":"chat"}}`, false},
		{"codex", `{"codex_provider":{"base_url":"https://example.test"}}`, false},
		{"mcode", `{"mcode_provider":{"options":{"baseURL":"https://example.test","apiKey":"secret-canary"},"models":{"other":{"limit":{"context":200000,"output":8000}}}}}`, false},
		{"mcode", `{"mcode_provider":{"npm":"@ai-sdk/openai","options":{"baseURL":"https://example.test","apiKey":"secret-canary"},"models":{"selected":{"limit":{"context":200000,"output":8000}}}}}`, false},
		{"mcode", `{"mcode_provider":{"enabled":false,"options":{"baseURL":"https://example.test","apiKey":"secret-canary"},"models":{"selected":{"limit":{"context":200000,"output":8000}}}}}`, false},
		{"mcode", `{"mcode_provider":{"enabled":"true","options":{"baseURL":"https://example.test","apiKey":"secret-canary"},"models":{"selected":{"limit":{"context":200000,"output":8000}}}}}`, false},
	} {
		resolver := deploymentModelDefaults(func(context.Context, store.Session) (map[string]any, error) {
			var opts map[string]any
			err := json.Unmarshal([]byte(tc.raw), &opts)
			return opts, err
		})
		provider, native, err := resolver(t.Context(), tc.harness, "selected")
		if tc.valid {
			if err != nil || provider != nil || native != nil {
				t.Fatal("omitted provider changed", err)
			}
		} else if err == nil || strings.Contains(err.Error(), "secret-canary") {
			t.Fatal("invalid provider accepted or secret leaked")
		}
	}
}
