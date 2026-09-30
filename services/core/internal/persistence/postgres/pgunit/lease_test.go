package pgunit

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgtest"
)

// The execution lease is database-wide, so every lease test owns a database.
func acquireLease(t *testing.T, pool *pgxpool.Pool) *Lease {
	t.Helper()
	lease, err := AcquireLease(t.Context(), pool)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lease.Close(context.Background()) })
	return lease
}

// awaitLeaseRelease waits for PostgreSQL to drop the advisory lock of a closed
// or terminated owner; local pgx cleanup does not acknowledge the release.
func awaitLeaseRelease(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var held bool
		// Match the single-bigint key in queries/scheduling.sql, scoped to this database.
		err := pool.QueryRow(t.Context(), `SELECT EXISTS (SELECT 1 FROM pg_locks WHERE locktype='advisory' AND granted AND objsubid=1
			AND classid::bigint * 4294967296 + objid::bigint = 706172736172
			AND database=(SELECT oid FROM pg_database WHERE datname=current_database()))`).Scan(&held)
		if err != nil {
			t.Fatal("observe execution lease release", err)
		}
		if !held {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("previous execution lease was not released")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestLeaseExcludesSecondOwnerUntilClosed(t *testing.T) {
	pool := pgtest.OpenIsolated(t, nil)
	lease := acquireLease(t, pool)
	if _, err := AcquireLease(t.Context(), pool); !errors.Is(err, ErrLeaseHeld) {
		t.Fatal("second owner acquired a held lease", err)
	}
	if err := lease.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, err := range []error{
		lease.Transaction(t.Context(), func(context.Context, pgx.Tx) error { return nil }),
		lease.CheckOwnership(t.Context()),
		lease.CancelOperations(t.Context(), func() { t.Error("closed lease cancelled operations") }),
	} {
		if !errors.Is(err, ErrLeaseClosed) {
			t.Fatal("closed lease kept writer authority", err)
		}
	}
	awaitLeaseRelease(t, pool)
	successor := acquireLease(t, pool)
	if err := successor.CheckOwnership(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestLeaseLossRejectsOperations(t *testing.T) {
	pool := pgtest.OpenIsolated(t, nil)
	lease := acquireLease(t, pool)
	var killed bool
	if err := pool.QueryRow(t.Context(), "SELECT pg_terminate_backend($1, 1000)", lease.conn.Conn().PgConn().PID()).Scan(&killed); err != nil || !killed {
		t.Fatal(killed, err)
	}
	operation, cancel := context.WithCancel(t.Context())
	defer cancel()
	if err := lease.CancelOperations(t.Context(), cancel); err == nil {
		t.Fatal("lost lease accepted cancellation fence")
	}
	if operation.Err() != nil {
		t.Fatal("lost lease ran the cancellation callback")
	}
	if err := lease.CheckOwnership(t.Context()); err == nil {
		t.Fatal("lost owner reported ownership")
	}
	if err := lease.Transaction(t.Context(), func(context.Context, pgx.Tx) error { return nil }); err == nil {
		t.Fatal("lost owner committed a transaction")
	}
	if err := lease.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := lease.CancelOperations(t.Context(), cancel); !errors.Is(err, ErrLeaseClosed) {
		t.Fatal("closed lease accepted cancellation fence", err)
	}
	awaitLeaseRelease(t, pool)
	successor := acquireLease(t, pool)
	if err := successor.CheckOwnership(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestLeaseSerializesOperations(t *testing.T) {
	pool := pgtest.OpenIsolated(t, nil)
	lease := acquireLease(t, pool)
	// A pgx connection rejects concurrent use, so these succeed only through the gate.
	var group sync.WaitGroup
	results := make(chan error, 16)
	for range 8 {
		group.Go(func() {
			results <- lease.Transaction(t.Context(), func(ctx context.Context, tx pgx.Tx) error {
				_, err := tx.Exec(ctx, "SELECT pg_sleep(0.01)")
				return err
			})
		})
		group.Go(func() { results <- lease.CheckOwnership(t.Context()) })
	}
	group.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	// A waiting operation honours its caller's cancellation.
	if err := lease.lock(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer lease.unlock()
	cancelled, stop := context.WithCancel(t.Context())
	stop()
	if err := lease.CheckOwnership(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatal("gate wait ignored cancellation", err)
	}
}

func TestLeaseTransactionDeadlineIncludesLockWait(t *testing.T) {
	pool := pgtest.OpenIsolated(t, nil)
	if _, err := pool.Exec(t.Context(), "CREATE TABLE lease_probe (id int PRIMARY KEY, value text NOT NULL); INSERT INTO lease_probe VALUES (1, 'before')"); err != nil {
		t.Fatal(err)
	}
	lease := acquireLease(t, pool)
	blocker, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(context.Background())
	if _, err = blocker.Exec(t.Context(), "SELECT id FROM lease_probe WHERE id=1 FOR UPDATE"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	start := time.Now()
	err = lease.Transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, "UPDATE lease_probe SET value='after' WHERE id=1")
		return err
	})
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) >= 7*time.Second {
		t.Fatal("leased transaction did not enforce its shorter deadline", err)
	}
	if err = blocker.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	var value string
	if err = pool.QueryRow(t.Context(), "SELECT value FROM lease_probe WHERE id=1").Scan(&value); err != nil || value != "before" {
		t.Fatal("timed-out transaction changed the row", value, err)
	}
}

func TestLeaseCancellationFenceHonorsBounds(t *testing.T) {
	pool := pgtest.OpenIsolated(t, nil)
	lease := acquireLease(t, pool)
	for _, test := range []struct {
		name           string
		limit, maximum time.Duration
	}{
		{"caller", 25 * time.Millisecond, time.Second},
		{"operation", 8 * time.Second, 6 * time.Second},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := lease.lock(t.Context()); err != nil {
				t.Fatal(err)
			}
			held := true
			defer func() {
				if held {
					lease.unlock()
				}
			}()
			operation, stopOperation := context.WithCancel(t.Context())
			defer stopOperation()
			ctx, cancel := context.WithTimeout(t.Context(), test.limit)
			defer cancel()
			started := time.Now()
			err := lease.CancelOperations(ctx, stopOperation)
			if !errors.Is(err, context.DeadlineExceeded) || time.Since(started) > test.maximum {
				t.Fatal("cancellation fence did not preserve its deadline", err, time.Since(started))
			}
			if operation.Err() != nil {
				t.Fatal("timed-out fence canceled operations outside the lease gate")
			}
			lease.unlock()
			held = false
			if err := lease.CheckOwnership(t.Context()); err != nil {
				t.Fatal("gate timeout damaged the healthy owner", err)
			}
			if err := lease.CancelOperations(t.Context(), stopOperation); err != nil {
				t.Fatal(err)
			}
			if !errors.Is(operation.Err(), context.Canceled) {
				t.Fatal("successful fence did not cancel synchronously")
			}
			if err := lease.CheckOwnership(t.Context()); err != nil {
				t.Fatal("cancellation damaged the healthy owner", err)
			}
		})
	}
}
