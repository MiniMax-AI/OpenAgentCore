package v1

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSavedProviderSafeView(t *testing.T) {
	input := &SavedAgentCoreInput{Harness: "codex", ModelProvider: &ModelProviderInput{
		Protocol: "responses", BaseURL: "https://example.test/v1", APIKey: "write-only-fixture",
		ContextWindow: 200000, MaxOutputTokens: 8000,
	}}
	if err := input.Validate(); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(input.SafeView())
	if err != nil || strings.Contains(string(raw), "write-only-fixture") || strings.Contains(string(raw), `"api_key":`) {
		t.Fatalf("unsafe view: %s %v", raw, err)
	}
	if !strings.Contains(string(raw), `"api_key_configured":true`) {
		t.Fatalf("missing credential status: %s", raw)
	}
	input.ModelProvider.APIKey = "changed"
	if input.SafeView().ModelProvider.BaseURL != "https://example.test/v1" {
		t.Fatal("missing safe endpoint")
	}
}

func TestSavedProviderExplicitHarnessCompatibility(t *testing.T) {
	for _, tc := range []struct {
		harness, protocol string
		valid             bool
	}{
		{"", "responses", true}, {"", "anthropic", true},
		{"codex", "responses", true}, {"codex", "anthropic", false},
		{"claude_sdk", "anthropic", true}, {"claude_sdk", "responses", false},
		{"mcode", "anthropic", true}, {"mcode", "responses", false},
	} {
		t.Run(tc.harness+"/"+tc.protocol, func(t *testing.T) {
			x := &SavedAgentCoreInput{Harness: tc.harness, ModelProvider: &ModelProviderInput{
				Protocol: tc.protocol, BaseURL: "https://example.test", APIKey: "fixture", ContextWindow: 100, MaxOutputTokens: 20,
			}}
			if err := x.Validate(); (err == nil) != tc.valid {
				t.Fatalf("valid=%v error=%v", tc.valid, err)
			}
		})
	}
	x := &SavedAgentCoreInput{Harness: "mcode", ModelProvider: &ModelProviderInput{Protocol: "anthropic", BaseURL: "https://example.test", APIKey: "fixture"}}
	if x.Validate() == nil {
		t.Fatal("MiniMax limits must be complete")
	}
}
