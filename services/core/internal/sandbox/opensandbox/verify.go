package opensandbox

import (
	"context"
	"errors"
	"time"

	osdk "github.com/alibaba/OpenSandbox/sdks/sandbox/go"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/providercontract"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

const maxCredentialReferences = 32

func (ConfigurationAdapter) DiscoverConfiguration(context.Context, sandbox.ConfigurationDiscoveryInput, sandbox.ProcessPaths) (sandbox.ConfigurationOptions, error) {
	return sandbox.ConfigurationOptions{}, &providercontract.UnsupportedError{Operation: "DiscoverConfiguration", Reason: ConfigurationAdapter{}.Requirements().Discovery.Reason}
}

func (ConfigurationAdapter) DiscoverSelection(context.Context, sandbox.DirectConfig) (sandbox.Selection, error) {
	return sandbox.Selection{}, &providercontract.UnsupportedError{Operation: "DiscoverSelection", Reason: ConfigurationAdapter{}.Requirements().SelectionDiscovery.Reason}
}

// VerifyCredential is read-only and bounded: the key must list sandboxes, and
// every Core-owned reference must resolve to sandboxes of this installation.
func (ConfigurationAdapter) VerifyCredential(ctx context.Context, c sandbox.DirectConfig, refs []sandbox.Reference) error {
	p, err := newProvider(c)
	if err != nil || len(refs) > maxCredentialReferences {
		return sandbox.ErrInvalid
	}
	for _, r := range refs {
		if !validReference(r) {
			return sandbox.ErrInvalid
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	// One installation-scoped page proves the key can read; each reference then
	// proves the sandboxes it names belong to this installation.
	if _, err := p.lifecycle.ListSandboxes(ctx, osdk.ListOptions{Metadata: map[string]string{keyInstallation: p.installation}, Page: 1, PageSize: 1}); err != nil {
		if errors.Is(nativeErr(err), sandbox.ErrCredentialRejected) {
			return sandbox.ErrCredentialRejected
		}
		return sandbox.ErrConfigurationUnconfirmed
	}
	for _, r := range refs {
		switch _, err := p.owned(ctx, r); {
		case errors.Is(err, sandbox.ErrOwnership):
			return sandbox.ErrCredentialOwnership
		case errors.Is(err, sandbox.ErrCredentialRejected):
			return err
		case err != nil:
			return sandbox.ErrConfigurationUnconfirmed
		}
	}
	return nil
}
