package harnessconfig

import "github.com/MiniMax-AI/OpenAgentCore/internal/modelprovider"

func (c Configuration) AcceptsHarnessConfig() bool { return c.ValidateNativeConfig != nil }

// ParseProvider validates the frozen bundle against this adapter's native
// declaration before launching a Harness. Unsupported snapshots are not rewritten.
func (c Configuration) ParseProvider(raw any) (modelprovider.Provider, error) {
	provider, err := modelprovider.ParseProvider(raw)
	if err != nil {
		return modelprovider.Provider{}, err
	}
	if err := c.Validate(string(provider.Protocol), provider.ContextWindow, provider.MaxOutputTokens); err != nil {
		return modelprovider.Provider{}, err
	}
	return provider, nil
}
