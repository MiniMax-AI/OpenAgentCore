package pgtest

import "testing"

func TestObserveExecutionLeaseReleaseWithNegativeAdvisoryLock(t *testing.T) {
	pool := OpenIsolated(t, nil)
	conn, err := pool.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	// Negative single-bigint keys occupy high unsigned pg_locks.classid bits.
	// Other tests may hold one on the same PostgreSQL server while we observe
	// this database's positive execution key.
	if _, err := conn.Exec(t.Context(), `SELECT pg_advisory_lock(-1::bigint),pg_advisory_lock(706172736172::bigint)`); err != nil {
		t.Fatal(err)
	}
	released := ObserveExecutionLeaseRelease(t, pool)
	if _, err := conn.Exec(t.Context(), `SELECT pg_advisory_unlock(706172736172::bigint)`); err != nil {
		t.Fatal(err)
	}
	released()
	if _, err := conn.Exec(t.Context(), `SELECT pg_advisory_unlock(-1::bigint)`); err != nil {
		t.Fatal(err)
	}
}
