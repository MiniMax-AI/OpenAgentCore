package pgtest

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ObserveExecutionLeaseRelease captures the current owner before shutdown. Local
// pgx cleanup does not acknowledge the server's release of its advisory lock.
func ObserveExecutionLeaseRelease(t *testing.T, pool *pgxpool.Pool) func() {
	t.Helper()
	// Match the single-bigint key in queries/scheduling.sql, scoped to this DB.
	const lock = `locktype='advisory' AND granted AND objsubid=1
		AND classid::bigint * 4294967296 + objid::bigint = 706172736172
		AND database=(SELECT oid FROM pg_database WHERE datname=current_database())`
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	var owner int32
	if err := pool.QueryRow(ctx, "SELECT pid FROM pg_locks WHERE "+lock).Scan(&owner); err != nil {
		t.Fatal("observe execution lease owner", err)
	}
	return func() {
		t.Helper()
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			var held bool
			if err := pool.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_locks WHERE "+lock+" AND pid=$1)", owner).Scan(&held); err != nil {
				t.Fatal("observe execution lease release", err)
			}
			if !held {
				return
			}
			select {
			case <-ctx.Done():
				t.Fatal("previous execution lease was not released", ctx.Err())
			case <-ticker.C:
			}
		}
	}
}
