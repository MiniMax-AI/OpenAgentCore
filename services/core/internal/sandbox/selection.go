package sandbox

import "context"

// Selection is the typed deployment configuration shared by preview and commit.
type Selection struct {
	DeploymentSpec
	ExpectedGeneration uint64        `json:"expected_generation"`
	Provider           string        `json:"provider"`
	Configuration      Configuration `json:"-"`
}

func (s Selection) HasCredential() bool {
	return s.Configuration != nil && s.Configuration.HasCredential()
}

func (s Selection) ReplacesCredential() bool {
	return s.Configuration != nil && s.Configuration.ReplacesCredential()
}

// SelectionDiscoverer is an optional read-only native configuration capability.
// It resolves candidate configuration; loading retained ownership never calls it.
type SelectionDiscoverer interface {
	DiscoverSelection(context.Context, Selection) (Selection, error)
}

// CredentialVerifier verifies access to already owned resources without mutation.
// Replacing credentials requires this capability and a shared CallFence.
type CredentialVerifier interface {
	VerifyCredential(context.Context, []Reference) error
}
