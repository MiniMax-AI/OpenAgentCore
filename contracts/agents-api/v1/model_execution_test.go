package v1

import "testing"

func TestModelExecutionValidation(t *testing.T) {
	for _, tc := range []struct {
		protocol, harness string
		valid             bool
	}{{"anthropic", "claude_sdk", true}, {"anthropic", "mcode", true}, {"responses", "codex", true}, {"responses", "mcode", false}, {"anthropic", "codex", false}, {"responses", "", false}} {
		p := ModelProviderInput{Protocol: tc.protocol, BaseURL: "https://example.com/v1", APIKey: "secret", ContextWindow: 200000, MaxOutputTokens: 8000}
		if (p.ValidateHarness(tc.harness) == nil) != tc.valid {
			t.Fatalf("incorrect protocol validation: %s/%s", tc.protocol, tc.harness)
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
