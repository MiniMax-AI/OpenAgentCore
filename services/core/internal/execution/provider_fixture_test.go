package execution

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxlink/relay"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/providercontract"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimegateway"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/providers"
)

type fakeProviderRegistry struct {
	t        testing.TB
	lookup   func(string) (providers.Adapter, error)
	build    func(sandbox.DirectConfig) (sandbox.SandboxProvider, error)
	discover func(context.Context, sandbox.DirectConfig) (sandbox.Selection, error)
	verify   func(context.Context, sandbox.DirectConfig, []sandbox.Reference) error
}

func (f *fakeProviderRegistry) Lookup(kind string) (providers.Adapter, error) {
	if f.lookup == nil {
		return providers.Adapter{}, unexpectedCall(f.t, "Lookup")
	}
	return f.lookup(kind)
}
func (f *fakeProviderRegistry) BuildDirect(c sandbox.DirectConfig) (sandbox.SandboxProvider, error) {
	if f.build == nil {
		return nil, unexpectedCall(f.t, "BuildDirect")
	}
	return f.build(c)
}
func (f *fakeProviderRegistry) DiscoverSelection(ctx context.Context, c sandbox.DirectConfig) (sandbox.Selection, error) {
	if f.discover == nil {
		return sandbox.Selection{}, unexpectedCall(f.t, "DiscoverSelection")
	}
	return f.discover(ctx, c)
}
func (f *fakeProviderRegistry) VerifyCredential(ctx context.Context, c sandbox.DirectConfig, refs []sandbox.Reference) error {
	if f.verify == nil {
		return unexpectedCall(f.t, "VerifyCredential")
	}
	return f.verify(ctx, c, refs)
}
func (f *fakeProviderRegistry) DiscoverConfiguration(context.Context, string, sandbox.ConfigurationDiscoveryInput, sandbox.ProcessPaths) (json.RawMessage, error) {
	return nil, unexpectedCall(f.t, "DiscoverConfiguration")
}

type fixtureNodeProviders struct {
	t        testing.TB
	provider sandbox.SandboxProvider
}

func (f fixtureNodeProviders) Proxy(string, providercontract.Operations, uint64) sandbox.SandboxProvider {
	if f.provider == nil {
		unexpectedCall(f.t, "Proxy")
	}
	return f.provider
}

func testManager(t *testing.T, owner Owner, deployments *deployment.Service, reader deployment.Reader, id string, p sandbox.SandboxProvider) (*runtimeManager, error) {
	t.Helper()
	registry := &fakeProviderRegistry{t: t, lookup: providers.Builtin().Lookup, build: func(sandbox.DirectConfig) (sandbox.SandboxProvider, error) {
		if p == nil {
			return nil, unexpectedCall(t, "BuildDirect")
		}
		return p, nil
	}, discover: func(_ context.Context, c sandbox.DirectConfig) (sandbox.Selection, error) { return c.Selection, nil }}
	d := &Dispatcher{Deployment: deployments, DeploymentReader: reader, Registry: runtimegateway.NewRegistry(), Links: relay.New(nil), InstallationID: id, SandboxLink: "wss://core.example/api/v1/sandbox-link", Providers: registry, NodeProviders: fixtureNodeProviders{t: t, provider: p}}
	return newRuntimeManager(owner, d, nil)
}

// selectTestProvider supplies a committed immutable setup and its adapter.
// Loading still uses the manager's real construction and publication path.
func selectTestProvider(t *testing.T, m *runtimeManager, config *RuntimeProvider) {
	t.Helper()
	setup := deployment.Setup{InstallationID: config.InstallationID, Provider: config.ProviderKind, Mode: config.Mode, Generation: config.Generation, BackendFingerprint: config.BackendFingerprint, Operations: config.Provider.ProviderOperations()}
	m.setups = &fakeDeploymentSetups{t: t, setup: committedSetup(&setup), allocationSetup: func(context.Context, sandbox.Reference) (deployment.Setup, error) { return setup, nil }}
	m.providers = &fakeProviderRegistry{t: t, build: func(sandbox.DirectConfig) (sandbox.SandboxProvider, error) { return config.Provider, nil }, lookup: providers.Builtin().Lookup}
	m.nodeProviders = fixtureNodeProviders{t: t, provider: config.Provider}
	m.sandboxLink = config.SandboxLink
}
