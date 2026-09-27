package localworkspace

import (
	"encoding/json"
	"errors"
	"os"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentnetwork"
)

// RuntimeNetworkPolicy reads deployment configuration, never caller options.
func RuntimeNetworkPolicy() (agentnetwork.Policy, error) {
	policy := agentnetwork.Policy{Access: os.Getenv("OAC_RUNTIME_NETWORK_ACCESS")}
	if raw := os.Getenv("OAC_RUNTIME_ALLOWED_DOMAINS"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &policy.AllowedDomains); err != nil {
			return policy, errors.New("invalid local Runtime network domains")
		}
	}
	// Unbound runtimes may serve operations without a hosted workspace.
	if policy.Access == "" && len(policy.AllowedDomains) == 0 {
		return policy, nil
	}
	if err := policy.Validate(); err != nil {
		return policy, errors.New("unsupported local Runtime network policy")
	}
	return policy, nil
}

// NetworkPolicy returns a copy of the frozen execution authority.
func (b *Binding) NetworkPolicy() agentnetwork.Policy {
	return agentnetwork.Policy{Access: b.networkAccess, AllowedDomains: append([]string(nil), b.allowedDomains...)}
}
