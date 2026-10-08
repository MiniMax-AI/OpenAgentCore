package integration

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxlink/relay"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/execution"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/modelconfigurationpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/sessionpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/providercontract"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimegateway"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/providers"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

// startWorker starts the execution Worker as cmd/server does: it acquires the
// execution lease on s's database and hands it, with the execution writer built
// on it, to the Worker, which closes it when Run exits. The Worker opens MCP
// bearer tokens through the vaults service on s, records model configuration
// observations through the model configuration adapter on s, runs Session use
// cases and reads through the Session service and adapter on s, and reads the
// deployment through the deployment adapter on s. Without the dispatcher's
// provider dependencies it uses a lifecycleProvider fixture.
func startWorker(t testing.TB, ctx context.Context, s *Store, dispatcher *execution.Dispatcher) *execution.Worker {
	t.Helper()
	worker, err := startWorkerErr(t, ctx, s, dispatcher)
	if err != nil {
		t.Fatal(err)
	}
	return worker
}

// startWorkerErr is startWorker for tests that assert a startup failure.
func startWorkerErr(t testing.TB, ctx context.Context, s *Store, dispatcher *execution.Dispatcher) (*execution.Worker, error) {
	lease, err := pgunit.AcquireLease(ctx, s.pool)
	if err != nil {
		return nil, err
	}
	owner, err := fixtureOwner(s, lease)
	if err != nil {
		return nil, errors.Join(err, lease.Close(ctx))
	}
	return startOwnedWorkerErr(t, ctx, s, dispatcher, owner)
}

// startOwnedWorker is startWorker on an Owner the test already holds, for tests
// that also run execution operations on it. The Worker closes its lease when
// Run exits.
func startOwnedWorker(t testing.TB, ctx context.Context, s *Store, dispatcher *execution.Dispatcher, owner execution.Owner) *execution.Worker {
	t.Helper()
	worker, err := startOwnedWorkerErr(t, ctx, s, dispatcher, owner)
	if err != nil {
		t.Fatal(err)
	}
	return worker
}

func startOwnedWorkerErr(t testing.TB, ctx context.Context, s *Store, dispatcher *execution.Dispatcher, owner execution.Owner) (*execution.Worker, error) {
	_, credentials, err := fixtureVaults(s)
	if err != nil {
		return nil, errors.Join(err, owner.Lease.Close(ctx))
	}
	service, err := newSessionService(s)
	if err != nil {
		return nil, errors.Join(err, owner.Lease.Close(ctx))
	}
	deployments, err := fixtureDeploymentService(s)
	if err != nil {
		return nil, errors.Join(err, owner.Lease.Close(ctx))
	}
	owned := *dispatcher
	owned.Credentials = credentials
	owned.Observer = modelconfigurationpg.New(pgunit.NewPool(s.pool), s.credentialCipher)
	owned.Deployment = deployments
	owned.DeploymentReader = deploymentStore(s)
	owned.Sessions = service
	owned.SessionsReader = sessionAdapter(s)
	if owned.Links == nil {
		owned.Links = relay.New(runtimegateway.NewLinkAuthority(sessionAdapter(s)))
	}
	if owned.InstallationID == "" {
		view, err := deployments.View(ctx)
		if err != nil {
			return nil, errors.Join(err, owner.Lease.Close(ctx))
		}
		owned.InstallationID = view.InstallationID
		if owned.InstallationID == "" {
			owned.InstallationID = testInstallation(t)
		}
	}
	if owned.Providers == nil && owned.NodeProviders == nil {
		p := &lifecycleProvider{resources: map[string]sandbox.Info{}}
		owned.Providers = &fixtureProviderRegistry{t: t, provider: p, lookup: providers.Builtin().Lookup}
		owned.NodeProviders = fixtureNodeProviders{t: t, provider: p}
	}
	if owned.SandboxLink == "" {
		owned.SandboxLink = "wss://core.invalid/api/v1/sandbox-link"
	}
	return execution.StartWorker(ctx, &owned, owner)
}

// testInstallation is the installation that owns the shared test database.
// The official-client acceptance runs Core as the same installation on that
// database, so both read it from tests/testdata/installation.id.
func testInstallation(t testing.TB) string {
	t.Helper()
	value, err := os.ReadFile("../testdata/installation.id")
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(value))
}

// executionOwner acquires the execution lease on s's database and builds the
// execution operations on it, for tests that run them without a Worker. The
// lease closes when the test ends.
func executionOwner(t testing.TB, s *Store) execution.Owner {
	t.Helper()
	lease, err := pgunit.AcquireLease(t.Context(), s.pool)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lease.Close(context.Background()) })
	owner, err := fixtureOwner(s, lease)
	if err != nil {
		t.Fatal(err)
	}
	return owner
}

// fixtureOwner builds the deployment and Session execution operations on
// lease, as cmd/server does.
func fixtureOwner(s *Store, lease *pgunit.Lease) (execution.Owner, error) {
	_, changes, err := fixtureDeploymentExecution(s, lease)
	if err != nil {
		return execution.Owner{}, err
	}
	sessionExecution, err := sessions.NewExecutionOperations(sessionpg.NewExecution(lease))
	if err != nil {
		return execution.Owner{}, err
	}
	return execution.Owner{
		Lease:      lease,
		Deployment: changes,
		Sessions:   sessionExecution,
	}, nil
}

// webDeployment claims s's deployment for a new installation and selects
// provider on it as Web setup does, then closes its execution lease so a
// Worker can start. It returns the installation.
func webDeployment(t *testing.T, s *Store, provider string) string {
	t.Helper()
	lease, err := pgunit.AcquireLease(t.Context(), s.pool)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close(context.Background())
	owner, err := fixtureOwner(s, lease)
	if err != nil {
		t.Fatal(err)
	}
	installation := uuid.NewString()
	input := sandbox.Selection{Provider: provider, DeploymentSpec: SandboxDeploymentTestSpec(provider)}
	if provider == "e2b" {
		input = e2bSelection()
	}
	if err := owner.Deployment.Claim(t.Context(), installation); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Deployment.Initialize(t.Context(), installation, input); err != nil {
		t.Fatal(err)
	}
	return installation
}

// fixtureProviderRegistry constructs the controlled provider through the same
// registry boundary as server startup. Setup declarations stay with the builtin registry.
type fixtureProviderRegistry struct {
	t        testing.TB
	provider sandbox.SandboxProvider
	lookup   func(string) (providers.Adapter, error)
}

func (f *fixtureProviderRegistry) Lookup(kind string) (providers.Adapter, error) {
	if f.lookup == nil {
		f.t.Errorf("unexpected provider lookup for %q", kind)
		return providers.Adapter{}, errors.New("unexpected provider lookup")
	}
	return f.lookup(kind)
}
func (f *fixtureProviderRegistry) BuildDirect(sandbox.DirectConfig) (sandbox.SandboxProvider, error) {
	if f.provider == nil {
		f.t.Error("unexpected direct provider construction")
		return nil, errors.New("unexpected direct provider construction")
	}
	return f.provider, nil
}
func (f *fixtureProviderRegistry) DiscoverSelection(_ context.Context, c sandbox.DirectConfig) (sandbox.Selection, error) {
	return c.Selection, nil
}
func (f *fixtureProviderRegistry) DiscoverConfiguration(context.Context, string, sandbox.ConfigurationDiscoveryInput, sandbox.ProcessPaths) (json.RawMessage, error) {
	f.t.Error("unexpected provider configuration discovery")
	return nil, errors.New("unexpected provider configuration discovery")
}
func (f *fixtureProviderRegistry) VerifyCredential(context.Context, sandbox.DirectConfig, []sandbox.Reference) error {
	f.t.Error("unexpected provider credential verification")
	return errors.New("unexpected provider credential verification")
}

type fixtureNodeProviders struct {
	t        testing.TB
	provider sandbox.SandboxProvider
}

func (f fixtureNodeProviders) Proxy(string, providercontract.Operations, uint64) sandbox.SandboxProvider {
	if f.provider == nil {
		f.t.Error("unexpected node provider construction")
		return nil
	}
	return f.provider
}

// webDispatcher uses the manager's committed setup loading and generation routing
// with controlled providers at the registry and node transport boundaries.
func webDispatcher(t testing.TB, installation string, p sandbox.SandboxProvider, registry *runtimegateway.Registry, links *relay.Relay) *execution.Dispatcher {
	return &execution.Dispatcher{Registry: registry, Links: links, InstallationID: installation, SandboxLink: "wss://core.invalid/api/v1/sandbox-link", Providers: &fixtureProviderRegistry{t: t, provider: p, lookup: providers.Builtin().Lookup}, NodeProviders: fixtureNodeProviders{t: t, provider: p}}
}

// startWebWorker starts the Worker on the committed deployment, with links as
// its Link relay or a new one when links is nil.
func startWebWorker(t *testing.T, s *Store, registry *runtimegateway.Registry, links *relay.Relay, installation string, p sandbox.SandboxProvider) *execution.Worker {
	t.Helper()
	w, err := startNextWorker(t, t.Context(), s, webDispatcher(t, installation, p, registry, links))
	if err != nil {
		t.Fatal(err)
	}
	return w
}

// startNextWorker is startWorkerErr after another owner closed its lease. A
// closed lease stays held until PostgreSQL ends its backend, so startup
// retries ErrLeaseHeld briefly.
func startNextWorker(t testing.TB, ctx context.Context, s *Store, dispatcher *execution.Dispatcher) (*execution.Worker, error) {
	for deadline := time.Now().Add(2 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		w, err := startWorkerErr(t, ctx, s, dispatcher)
		if !errors.Is(err, pgunit.ErrLeaseHeld) || time.Now().After(deadline) {
			return w, err
		}
	}
}
