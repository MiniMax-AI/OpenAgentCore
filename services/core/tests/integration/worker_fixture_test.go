package integration

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxlink/relay"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/execution"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/modelconfigurationpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/sessionpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimegateway"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

// startWorker starts the execution Worker as cmd/server does: it acquires the
// execution lease on s's database and hands it, with the execution writer built
// on it, to the Worker, which closes it when Run exits. The Worker opens MCP
// bearer tokens through the vaults service on s, records model configuration
// observations through the model configuration adapter on s, runs Session use
// cases and reads through the Session service and adapter on s, and reads the
// deployment through the deployment adapter on s. Without the dispatcher's
// sandbox runtimes it runs fixtureRuntimes.
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
	if owned.ManagedRuntimes == nil {
		owned.ManagedRuntimes = fixtureRuntimes(t, s)
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

// fixtureRuntimes is webRuntimes on a lifecycleProvider for the installation
// that claimed s's database, or for testInstallation before one did. On an
// unconfigured deployment it never loads a provider.
func fixtureRuntimes(t testing.TB, s *Store) *execution.RuntimeProvider {
	t.Helper()
	view, err := deploymentService(t, s).View(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	installation := view.InstallationID
	if installation == "" {
		installation = testInstallation(t)
	}
	return webRuntimes(t, s, installation, &lifecycleProvider{resources: map[string]sandbox.Info{}}, nil)
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

// webRuntimes is the Worker's sandbox runtimes as cmd/server builds them for
// installation: a deferred provider that runs s's committed Web setup on p.
func webRuntimes(t testing.TB, s *Store, installation string, p sandbox.SandboxProvider, suspension *execution.RuntimeSuspensionPolicy) *execution.RuntimeProvider {
	deployments := deploymentService(t, s)
	return execution.NewDeferredRuntimeProvider(installation, func(ctx context.Context) (*execution.RuntimeProvider, error) {
		setup, err := deployments.Setup(ctx)
		if err != nil || setup.Provider == "" {
			return nil, err
		}
		return &execution.RuntimeProvider{InstallationID: setup.InstallationID, ProviderKind: setup.Provider, Mode: setup.Mode, Generation: setup.Generation,
			CoreURL: "http://core.invalid/api/v1", SandboxLink: "wss://core.invalid/api/v1/sandbox-link", BackendFingerprint: setup.BackendFingerprint, Provider: p, Suspension: suspension}, nil
	}, unusedPreparation(t))
}

// unusedPreparation is the preparer of a test that submits no sandbox
// selection through the Worker; preparing one fails the test.
func unusedPreparation(t testing.TB) execution.RuntimeDeploymentPreparer {
	return func(context.Context, deployment.Setup) (execution.PreparedRuntimeDeployment, error) {
		t.Error("the test prepared a sandbox selection it did not submit")
		return execution.PreparedRuntimeDeployment{}, errors.New("unexpected sandbox selection preparation")
	}
}

// startWebWorker starts the Worker on webRuntimes, with links as its Link
// relay or a new one when links is nil.
func startWebWorker(t *testing.T, s *Store, registry *runtimegateway.Registry, links *relay.Relay, installation string, p sandbox.SandboxProvider, suspension *execution.RuntimeSuspensionPolicy) *execution.Worker {
	t.Helper()
	w, err := startNextWorker(t, t.Context(), s, &execution.Dispatcher{Registry: registry, Links: links, ManagedRuntimes: webRuntimes(t, s, installation, p, suspension)})
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
