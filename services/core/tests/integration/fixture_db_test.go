package integration

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/execution"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/deploymentpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/modelconfigurationpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/sessionpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

// fixtureDB is the database and credential key that built the test's Store.
type fixtureDB struct {
	pool      *pgxpool.Pool
	cipher    *credentialcrypto.Cipher // nil for a keyless Store
	publicURL string                   // the public URL of the Store's placement rules, if any
}

// newTestStoreDB is testStore with the fixtureDB that built it.
func newTestStoreDB(t *testing.T) (*Store, fixtureDB) {
	s, pool := testStore(t)
	return s, fixtureDB{pool: pool}
}

// newModelTestStoreDB is NewModelTestStore with the fixtureDB that built it.
func newModelTestStoreDB(t *testing.T) (*Store, fixtureDB) {
	s, pool := NewModelTestStore(t)
	return s, fixtureDB{pool: pool, cipher: fixtureCipher}
}

// newManagedTestStoreDB is newManagedTestStore with the fixtureDB that built it.
func newManagedTestStoreDB(t *testing.T) (*Store, fixtureDB) {
	s, pool := newManagedTestStore(t)
	return s, fixtureDB{pool: pool, cipher: fixtureCipher}
}

// startWorker starts the execution Worker as cmd/server does: it acquires the
// execution lease on db and hands it, with the execution writer built on it, to
// the Worker, which closes it when Run exits. The Worker opens MCP bearer tokens
// through the vaults service on db, records model configuration observations
// through the model configuration adapter on db, runs Session use cases and
// reads through the Session service and adapter on db, and reads the
// deployment through the deployment adapter on db.
func startWorker(t testing.TB, ctx context.Context, db fixtureDB, dispatcher *execution.Dispatcher) *execution.Worker {
	t.Helper()
	worker, err := startWorkerErr(ctx, db, dispatcher)
	if err != nil {
		t.Fatal(err)
	}
	return worker
}

// startWorkerErr is startWorker for tests that assert a startup failure.
func startWorkerErr(ctx context.Context, db fixtureDB, dispatcher *execution.Dispatcher) (*execution.Worker, error) {
	lease, err := pgunit.AcquireLease(ctx, db.pool)
	if err != nil {
		return nil, err
	}
	owner, err := fixtureOwner(db, lease)
	if err != nil {
		return nil, errors.Join(err, lease.Close(ctx))
	}
	return startOwnedWorkerErr(ctx, db, dispatcher, owner)
}

// startOwnedWorker is startWorker on an Owner the test already holds, for tests
// that also run execution operations on it. The Worker closes its lease when
// Run exits.
func startOwnedWorker(t testing.TB, ctx context.Context, db fixtureDB, dispatcher *execution.Dispatcher, owner execution.Owner) *execution.Worker {
	t.Helper()
	worker, err := startOwnedWorkerErr(ctx, db, dispatcher, owner)
	if err != nil {
		t.Fatal(err)
	}
	return worker
}

func startOwnedWorkerErr(ctx context.Context, db fixtureDB, dispatcher *execution.Dispatcher, owner execution.Owner) (*execution.Worker, error) {
	_, credentials, err := fixtureVaults(db)
	if err != nil {
		return nil, errors.Join(err, owner.Lease.Close(ctx))
	}
	sessionStore, sessionService, err := fixtureSessions(db)
	if err != nil {
		return nil, errors.Join(err, owner.Lease.Close(ctx))
	}
	deployments, err := fixtureDeploymentService(db)
	if err != nil {
		return nil, errors.Join(err, owner.Lease.Close(ctx))
	}
	owned := *dispatcher
	owned.Credentials = credentials
	owned.Observer = modelconfigurationpg.New(pgunit.NewPool(db.pool), db.cipher)
	owned.Deployment = deployments
	owned.DeploymentReader = deploymentpg.New(pgunit.NewPool(db.pool), db.cipher)
	owned.Sessions = sessionService
	owned.SessionsReader = sessionStore
	return execution.StartWorker(ctx, &owned, owner)
}

// executionOwner acquires the execution lease on db and builds the execution
// operations on it, for tests that run them without a Worker. The lease
// closes when the test ends.
func executionOwner(t testing.TB, db fixtureDB) execution.Owner {
	t.Helper()
	lease, err := pgunit.AcquireLease(t.Context(), db.pool)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lease.Close(context.Background()) })
	owner, err := fixtureOwner(db, lease)
	if err != nil {
		t.Fatal(err)
	}
	return owner
}

// fixtureOwner builds the deployment and Session execution operations on
// lease, as cmd/server does.
func fixtureOwner(db fixtureDB, lease *pgunit.Lease) (execution.Owner, error) {
	_, changes, err := fixtureDeploymentExecution(db, lease)
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
