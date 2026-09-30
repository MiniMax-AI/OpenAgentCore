package providers

import (
	"encoding/json"
	"errors"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/providercontract"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"testing"
)

func TestNodeConfigurationExplicitUnsupportedAndStrictEmptyInput(t *testing.T) {
	for _, kind := range []string{"docker", "microsandbox"} {
		a, err := Lookup(kind)
		if err != nil {
			t.Fatal(err)
		}
		for _, raw := range []string{`null`, `[]`, `{"secret":"private"}`, `{"template":"x"}`} {
			if _, err := a.Configuration.DecodeInput(json.RawMessage(raw), nil); !errors.Is(err, sandbox.ErrInvalid) {
				t.Fatal(kind, "accepted configuration", err)
			}
		}
		for _, secret := range []string{`{}`, `null`, `{"api_key":"private"}`} {
			if _, err := a.Configuration.DecodeInput(nil, json.RawMessage(secret)); !errors.Is(err, sandbox.ErrInvalid) {
				t.Fatal(kind, "accepted credential", err)
			}
		}
		discover, ok := a.Configuration.(sandbox.ConfigurationDiscoverer)
		if !ok {
			t.Fatal("missing explicit discovery implementation")
		}
		if _, err := discover.DiscoverConfiguration(t.Context(), sandbox.ConfigurationDiscoveryInput{}); !errors.Is(err, providercontract.ErrUnsupported) {
			t.Fatal("discovery did not reject", err)
		}
		if _, err := a.Configuration.WithCredential(nil, nil); !errors.Is(err, providercontract.ErrUnsupported) {
			t.Fatal("credential replacement did not reject", err)
		}
	}
}
