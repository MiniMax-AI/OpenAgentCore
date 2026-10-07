package builtin

import (
	"reflect"
	"strings"
	"testing"
)

func TestNativeProviderDeclarationIsSharedWithRuntime(t *testing.T) {
	expected := map[string][]string{"codex": {"responses"}, "claude_sdk": {"anthropic"}, "mcode": {"anthropic", "responses", "chat_completions"}}
	for kind, protocols := range expected {
		t.Run(kind, func(t *testing.T) {
			config, ok := Registry().Lookup(kind)
			if !ok {
				t.Fatal("missing declaration")
			}
			var declared []string
			for _, p := range config.Providers {
				declared = append(declared, p.Protocol)
			}
			if !reflect.DeepEqual(protocols, declared) {
				t.Fatalf("protocols: %v", declared)
			}
			if config.AcceptsHarnessConfig() != (kind != "mcode") {
				t.Fatal("native configuration support drift")
			}
			for _, protocol := range []string{"anthropic", "responses", "chat_completions"} {
				raw := map[string]any{"protocol": protocol, "base_url": "https://model.example", "api_key": "key-canary", "context_window": 64000, "max_output_tokens": 4096}
				p, err := config.ParseProvider(raw)
				accepted := kind == "mcode" || (kind == "codex" && protocol == "responses") || (kind == "claude_sdk" && protocol == "anthropic")
				if (err == nil) != accepted || (config.ValidateProtocol(protocol) == nil) != accepted {
					t.Fatalf("%s: native admission mismatch", protocol)
				}
				if err != nil && strings.Contains(err.Error(), "key-canary") {
					t.Fatal("secret in error")
				}
				if accepted && (string(p.Protocol) != protocol || p.BaseURL != raw["base_url"] || p.APIKey != raw["api_key"]) {
					t.Fatal("native bundle was rewritten")
				}
			}
		})
	}
}
