package sandbox

import "context"

// APIKey is internal configuration. HTTP requests use a write-only DTO.
// TemplateBuild is set only by Core after it validates the candidate.
type E2BConfiguration struct {
	ReplaceCredential bool           `json:"-"`
	APIKey            string         `json:"-"`
	APIURL            string         `json:"api_url,omitempty"`
	Domain            string         `json:"domain,omitempty"`
	Template          string         `json:"template"`
	TemplateBuild     *TemplateBuild `json:"-"`
}

// TemplateBuild is the fixed build as read by the validation that
// admitted a selection. RootDiskMiB is nil when E2B does not report it.
type TemplateBuild struct {
	Status          string
	CPUs, MemoryMiB int32
	RootDiskMiB     *int32
}

// Selection is the typed deployment configuration shared by preview and commit.
type Selection struct {
	DeploymentSpec
	ExpectedGeneration uint64            `json:"expected_generation"`
	Provider           string            `json:"provider"`
	E2B                *E2BConfiguration `json:"e2b,omitempty"`
}

func (s Selection) HasCredential() bool { return s.E2B != nil }

func (s Selection) ReplacesCredential() bool { return s.E2B != nil && s.E2B.ReplaceCredential }

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
