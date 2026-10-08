package v1

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/modelprovider"
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
	for _, harness := range []string{"", "codex", "claude_sdk", "mcode"} {
		for _, protocol := range []string{"anthropic", "responses", "chat_completions"} {
			t.Run(harness+"/"+protocol, func(t *testing.T) {
				x := &SavedAgentCoreInput{Harness: harness, ModelProvider: &ModelProviderInput{
					Protocol: modelprovider.Protocol(protocol), BaseURL: "https://example.test", APIKey: "fixture", ContextWindow: 100, MaxOutputTokens: 20,
				}}
				native := harness == "" || harness == "mcode" || (harness == "codex" && protocol == "responses") || (harness == "claude_sdk" && protocol == "anthropic")
				if err := x.Validate(); (err == nil) != native {
					t.Fatalf("wrong saved native admission: %v", err)
				}
				if harness != "" {
					if err := x.SafeView().ModelProvider.ValidateHarness(harness); (err == nil) != native {
						t.Fatalf("safe saved provider rejected: %v", err)
					}
				}
			})
		}
	}
	for _, protocol := range []string{"anthropic", "responses", "chat_completions"} {
		for _, limits := range [][2]int32{{0, 0}, {100, 0}, {0, 20}} {
			x := &SavedAgentCoreInput{Harness: "mcode", ModelProvider: &ModelProviderInput{
				Protocol: modelprovider.Protocol(protocol), BaseURL: "https://example.test", APIKey: "fixture", ContextWindow: limits[0], MaxOutputTokens: limits[1],
			}}
			if x.Validate() == nil || x.SafeView().ModelProvider.ValidateHarness("mcode") == nil {
				t.Fatal("MiniMax limits must be complete for every upstream protocol")
			}
		}
	}
}
