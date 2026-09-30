package store_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/execution"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
)

// fixtureDB is the database and credential key that built the test's Store.
// Cutovers build their adapters from it; add fields here, never parameters.
type fixtureDB struct {
	pool   *pgxpool.Pool
	cipher *credentialcrypto.Cipher // nil for a keyless Store
}

// newTestStoreDB is store.NewTestStore with the fixtureDB that built it.
func newTestStoreDB(t *testing.T) (*store.Store, fixtureDB) {
	s, pool := store.NewTestStore(t)
	return s, fixtureDB{pool: pool}
}

// newModelTestStoreDB is store.NewModelTestStore with the fixtureDB that built it.
func newModelTestStoreDB(t *testing.T) (*store.Store, fixtureDB) {
	s, pool := store.NewModelTestStore(t)
	return s, fixtureDB{pool: pool, cipher: store.FixtureCipher()}
}

// newManagedTestStoreDB is store.NewManagedTestStore with the fixtureDB that built it.
func newManagedTestStoreDB(t *testing.T) (*store.Store, fixtureDB) {
	s, pool := store.NewManagedTestStore(t)
	return s, fixtureDB{pool: pool, cipher: store.FixtureCipher()}
}

// startWorker starts the execution Worker as cmd/server does: it acquires the
// execution lease on db and hands it, with the execution writer built on it, to
// the Worker, which closes it when Run exits.
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
	return execution.StartWorker(ctx, dispatcher, execution.Owner{Lease: lease, Store: store.NewExecution(dispatcher.Store, lease)})
}

// executionOwner acquires the execution lease on db and builds s's execution
// writer on it, for tests that run execution operations without a Worker. The
// lease closes when the test ends.
func executionOwner(t testing.TB, db fixtureDB, s *store.Store) execution.Owner {
	t.Helper()
	lease, err := pgunit.AcquireLease(t.Context(), db.pool)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lease.Close(context.Background()) })
	return execution.Owner{Lease: lease, Store: store.NewExecution(s, lease)}
}
