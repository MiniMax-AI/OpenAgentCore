package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestDiagnosticSnapshotCancellationReleasesTransaction(t *testing.T) {
	for _, rootTurn := range []bool{false, true} {
		name, table := "session", "environments"
		if rootTurn {
			name, table = "turn", "session_items"
		}
		t.Run(name, func(t *testing.T) {
			s, pool := testStore(t)
			tenant, session := newTurnSession(t, s)
			receipt := submitMessage(t, s, tenant, session.ID, "input")
			blocker, err := pool.Begin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer blocker.Rollback(context.Background())
			if _, err = blocker.Exec(t.Context(), "LOCK TABLE "+table+" IN ACCESS EXCLUSIVE MODE"); err != nil {
				t.Fatal(err)
			}
			// The first Session/Turn lookup establishes a snapshot before the blocked
			// relation read. A short caller deadline must release it while the lock stays held.
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() {
				var err error
				if rootTurn {
					_, err = s.GetTurnDiagnosticsSnapshot(ctx, tenant, session.ID, receipt.TurnID)
				} else {
					_, err = s.GetSessionDiagnosticsSnapshot(ctx, tenant, session.ID)
				}
				done <- err
			}()
			var readerPID int
			for readerPID == 0 {
				err = pool.QueryRow(t.Context(), `SELECT COALESCE((SELECT pid FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND backend_xmin IS NOT NULL AND query LIKE $1 LIMIT 1),0)`, "%FROM "+table+"%").Scan(&readerPID)
				if err != nil {
					t.Fatal(err)
				}
				if readerPID != 0 {
					break
				}
				select {
				case err := <-done:
					t.Fatal("snapshot did not reach the blocked read", err)
				case <-time.After(5 * time.Millisecond):
				}
			}
			select {
			case err := <-done:
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatal("short caller deadline was not preserved", err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("snapshot read ignored cancellation")
			}
			cleanup, stop := context.WithTimeout(t.Context(), 2*time.Second)
			defer stop()
			for {
				var retained bool
				err = pool.QueryRow(cleanup, "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE pid=$1 AND xact_start IS NOT NULL)", readerPID).Scan(&retained)
				if err != nil {
					t.Fatal("cancelled snapshot retained its transaction", err)
				}
				if !retained {
					break
				}
				time.Sleep(5 * time.Millisecond)
			}
			// Cleanup is checked before releasing the blocker: no reader transaction
			// may depend on the conflicting lock becoming available for cleanup.
			if err = blocker.Rollback(t.Context()); err != nil {
				t.Fatal(err)
			}
			if rootTurn {
				_, err = s.GetTurnDiagnosticsSnapshot(t.Context(), tenant, session.ID, receipt.TurnID)
			} else {
				_, err = s.GetSessionDiagnosticsSnapshot(t.Context(), tenant, session.ID)
			}
			if err != nil {
				t.Fatal("snapshot read did not recover", err)
			}
		})
	}
}
