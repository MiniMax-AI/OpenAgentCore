package integration

import (
	"context"
	"errors"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxlink/relay"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/execution"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/modelconfigurationpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/sessionpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimegateway"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

// startWorker starts the execution Worker as cmd/server does: it acquires the
// execution lease on s's database and hands it, with the execution writer built
// on it, to the Worker, which closes it when Run exits. The Worker opens MCP
// bearer tokens through the vaults service on s, records model configuration
// observations through the model configuration adapter on s, runs Session use
// cases and reads through the Session service and adapter on s, and reads the
// deployment through the deployment adapter on s.
func startWorker(t testing.TB, ctx context.Context, s *Store, dispatcher *execution.Dispatcher) *execution.Worker {
	t.Helper()
	worker, err := startWorkerErr(ctx, s, dispatcher)
	if err != nil {
		t.Fatal(err)
	}
	return worker
}

// startWorkerErr is startWorker for tests that assert a startup failure.
func startWorkerErr(ctx context.Context, s *Store, dispatcher *execution.Dispatcher) (*execution.Worker, error) {
	lease, err := pgunit.AcquireLease(ctx, s.pool)
	if err != nil {
		return nil, err
	}
	owner, err := fixtureOwner(s, lease)
	if err != nil {
		return nil, errors.Join(err, lease.Close(ctx))
	}
	return startOwnedWorkerErr(ctx, s, dispatcher, owner)
}

// startOwnedWorker is startWorker on an Owner the test already holds, for tests
// that also run execution operations on it. The Worker closes its lease when
// Run exits.
func startOwnedWorker(t testing.TB, ctx context.Context, s *Store, dispatcher *execution.Dispatcher, owner execution.Owner) *execution.Worker {
	t.Helper()
	worker, err := startOwnedWorkerErr(ctx, s, dispatcher, owner)
	if err != nil {
		t.Fatal(err)
	}
	return worker
}

func startOwnedWorkerErr(ctx context.Context, s *Store, dispatcher *execution.Dispatcher, owner execution.Owner) (*execution.Worker, error) {
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
	return execution.StartWorker(ctx, &owned, owner)
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
