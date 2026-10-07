package builtin

import (
	"reflect"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/modelprovider"
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
				raw := modelprovider.Provider{Protocol: modelprovider.Protocol(protocol), BaseURL: "https://model.example", APIKey: "key-canary", ContextWindow: 64000, MaxOutputTokens: 4096}
				prepared, err := config.Prepare(proto.PromptRequestPayload{Model: "fixture", ModelProvider: &raw})
				accepted := kind == "mcode" || (kind == "codex" && protocol == "responses") || (kind == "claude_sdk" && protocol == "anthropic")
				if (err == nil) != accepted || (config.ValidateProtocol(protocol) == nil) != accepted {
					t.Fatalf("%s: native admission mismatch", protocol)
				}
				if err != nil && strings.Contains(err.Error(), "key-canary") {
					t.Fatal("secret in error")
				}
				if accepted && prepared.Provider != raw {
					t.Fatal("native bundle was rewritten")
				}
			}
		})
	}
}
