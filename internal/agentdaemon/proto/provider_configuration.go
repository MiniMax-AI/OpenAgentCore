package proto

import "slices"

// AgentProviderConfiguration describes the adapter build's provider inputs,
// independently of binary availability and live operation admission. Older
// peers may omit this optional descriptor, which means unknown support.
type AgentProviderConfiguration struct {
	SchemaVersion int                     `json:"schema_version"`
	Providers     []AgentProviderProtocol `json:"providers"`
}

type AgentProviderProtocol struct {
	Protocol            string `json:"protocol"`
	RequiresTokenLimits bool   `json:"requires_token_limits"`
}

func (c *AgentProviderConfiguration) Clone() *AgentProviderConfiguration {
	if c == nil {
		return nil
	}
	copy := *c
	copy.Providers = slices.Clone(c.Providers)
	return &copy
}
