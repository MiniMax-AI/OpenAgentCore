package harnessconfig

import (
	"errors"

	"github.com/MiniMax-AI/OpenAgentCore/internal/modelprovider"
)

func (c Configuration) AcceptsHarnessConfig() bool { return c.ValidateNativeConfig != nil }

// ParseProvider validates the frozen bundle against this adapter's native
// declaration before launching a Harness. Unsupported snapshots are not rewritten.
func (c Configuration) ParseProvider(raw any) (modelprovider.Provider, error) {
	provider, err := modelprovider.ParseProvider(raw)
	if err != nil {
		return modelprovider.Provider{}, err
	}
	if err := c.ValidateProtocol(string(provider.Protocol)); err != nil {
		return modelprovider.Provider{}, err
	}
	if err := c.ValidateMaxOutputTokens(provider.MaxOutputTokens); err != nil {
		return modelprovider.Provider{}, err
	}
	return provider, nil
}

// ValidateMaxOutputTokens rejects a set limit the Harness has no native home for.
func (c Configuration) ValidateMaxOutputTokens(value int32) error {
	if value > 0 && !c.MaxOutputTokens {
		return errors.New("The selected harness has no native max_output_tokens setting.")
	}
	return nil
}
