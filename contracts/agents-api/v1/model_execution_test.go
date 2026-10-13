package v1

import (
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/modelprovider"
)

func TestModelExecutionValidation(t *testing.T) {
	for _, harness := range []string{"codex", "claude_sdk", "mcode"} {
		for _, protocol := range []string{"anthropic", "responses", "chat_completions"} {
			t.Run(harness+"/"+protocol, func(t *testing.T) {
				p := ModelProviderInput{Protocol: modelprovider.Protocol(protocol), BaseURL: "https://example.com/v1", APIKey: "secret", ContextWindow: 200000, MaxOutputTokens: 8000}
				native := harness == "mcode" || (harness == "codex" && protocol == "responses") || (harness == "claude_sdk" && protocol == "anthropic")
				if err := p.ValidateHarness(harness); (err == nil) != native {
					t.Fatalf("wrong native protocol admission: %v", err)
				}
				p.ContextWindow, p.MaxOutputTokens = 0, 0
				if err := p.ValidateHarness(harness); (err == nil) != (native && harness != "mcode") {
					t.Fatalf("incorrect optional token-limit admission: %v", err)
				}
			})
		}
	}
	for _, tc := range []struct{ protocol, harness string }{
		{"responses", ""}, {"responses", "unknown"}, {"unknown", "codex"},
		{"openai", "codex"}, {"chat", "claude_sdk"}, {"chat-completions", "mcode"},
	} {
		p := ModelProviderInput{Protocol: modelprovider.Protocol(tc.protocol), BaseURL: "https://example.com/v1", APIKey: "secret", ContextWindow: 200000, MaxOutputTokens: 8000}
		if p.ValidateHarness(tc.harness) == nil {
			t.Fatalf("unsupported protocol or harness accepted: %s/%s", tc.protocol, tc.harness)
		}
	}
	for _, url := range []string{"http://example.com", "https://user:pass@example.com", "https://example.com?key=secret", "https://example.com#secret", "https://",
		"https://example.com:99999/v1", "https://example.com:0/v1", "https://xn--.test", "https://xn--a.test", "https://a..b", "https://999.1.1.1"} {
		if (&ModelProviderInput{Protocol: "responses", BaseURL: url, APIKey: "secret"}).Validate() == nil {
			t.Fatal("unsafe or unusable provider URL accepted", url)
		}
	}
	for _, url := range []string{"https://example.com:8443/v1", "https://127.0.0.1/v1", "https://[::1]:8443/v1", "https://model_gateway.internal/v1", "https://bücher.example/v1"} {
		if err := (&ModelProviderInput{Protocol: "responses", BaseURL: url, APIKey: "secret"}).Validate(); err != nil {
			t.Fatal("valid provider URL rejected", url, err)
		}
	}
	if (&ModelProviderInput{Protocol: "anthropic", BaseURL: "https://example.com", APIKey: "secret"}).ValidateHarness("mcode") == nil {
		t.Fatal("MiniMax Code accepted unknown model limits")
	}
}
