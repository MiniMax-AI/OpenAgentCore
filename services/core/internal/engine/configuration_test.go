package engine

import (
	"testing"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig/builtin"
)

func TestProviderDeclarationsAgreeWithAdmission(t *testing.T) {
	for _, kind := range (Catalog{}).Kinds() {
		t.Run(kind, func(t *testing.T) {
			declared, ok := builtin.Registry().Lookup(kind)
			if !ok {
				t.Fatal("qualified harness has no provider declaration")
			}
			for _, protocol := range []string{"responses", "anthropic", "unknown"} {
				provider, supported := declared.Provider(protocol)
				input := v1.ModelProviderInput{Protocol: protocol, BaseURL: "https://example.test", APIKey: "private-fixture", ContextWindow: 100, MaxOutputTokens: 20}
				if (input.ValidateHarness(kind) == nil) != supported || (v1.ValidateModelProtocol(protocol, kind) == nil) != supported {
					t.Fatalf("protocol %q disagrees with validation", protocol)
				}
				if !supported {
					continue
				}
				input.ContextWindow, input.MaxOutputTokens = 0, 0
				if (input.ValidateHarness(kind) != nil) != provider.RequiresTokenLimits {
					t.Fatalf("token requirements disagree for %q", protocol)
				}
				if provider.RequiresTokenLimits {
					input.ContextWindow = 100
					if input.ValidateHarness(kind) == nil {
						t.Fatal("missing output limit accepted")
					}
				}
			}
		})
	}
}
