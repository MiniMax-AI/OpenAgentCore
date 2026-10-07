package v1

import "testing"

func TestModelExecutionValidation(t *testing.T) {
	for _, harness := range []string{"codex", "claude_sdk", "mcode"} {
		for _, protocol := range []string{"anthropic", "responses", "chat_completions"} {
			t.Run(harness+"/"+protocol, func(t *testing.T) {
				p := ModelProviderInput{Protocol: protocol, BaseURL: "https://example.com", APIKey: "secret", ContextWindow: 200000, MaxOutputTokens: 8000}
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
		p := ModelProviderInput{Protocol: tc.protocol, BaseURL: "https://example.com/v1", APIKey: "secret", ContextWindow: 200000, MaxOutputTokens: 8000}
		if p.ValidateHarness(tc.harness) == nil {
			t.Fatalf("unsupported protocol or harness accepted: %s/%s", tc.protocol, tc.harness)
		}
	}
	if (&ModelProviderInput{Protocol: "anthropic", BaseURL: "https://example.com", APIKey: "secret"}).ValidateHarness("mcode") == nil {
		t.Fatal("MiniMax Code accepted unknown model limits")
	}
}
