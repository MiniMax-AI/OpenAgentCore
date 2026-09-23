package node

import (
	"context"
	"errors"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Real PostgreSQL row locks exercise the same synchronous, cancellable pgx
// callbacks used by Core, including transactional presence and per-node cleanup
// locking. Each run owns a unique table, never runtime data.
func TestHubPostgresBlockedOpeningIsBounded(t *testing.T) {
	dsn := os.Getenv("PARSAR_AGENTS_API_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("dedicated PostgreSQL required")
	}
	pool, err := pgxpool.New(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	for _, mode := range []string{"deadline", "close"} {
		t.Run(mode, func(t *testing.T) {
			ctx := t.Context()
			table := pgx.Identifier{"node_hub_" + strings.ReplaceAll(uuid.NewString(), "-", "")}.Sanitize()
			if _, err := pool.Exec(ctx, "CREATE TABLE "+table+" (id text primary key, connection text)"); err != nil {
				t.Fatal(err)
			}
			defer func() {
				cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if _, err := pool.Exec(cleanup, "DROP TABLE "+table); err != nil {
					t.Error(err)
				}
			}()
			fast, slow := identity(), identity()
			if _, err := pool.Exec(ctx, "INSERT INTO "+table+" VALUES ($1,NULL)", slow.NodeID); err != nil {
				t.Fatal(err)
			}
			lock, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer lock.Rollback(context.Background())
			if _, err = lock.Exec(ctx, "UPDATE "+table+" SET connection=NULL WHERE id=$1", slow.NodeID); err != nil {
				t.Fatal(err)
			}
			pids, results := make(chan uint32, 1), make(chan error, 1)
			var attempts atomic.Int32
			hub := NewHub(HubOptions{
				Authenticate: func(_ context.Context, id, _ string) (Identity, error) {
					if id == slow.NodeID {
						return slow, nil
					}
					return fast, nil
				},
				OwnerEpoch: func(context.Context) (uint64, error) { return 3, nil },
				Connected: func(ctx context.Context, id Identity, connection string, _ uint64) error {
					if id.NodeID != slow.NodeID {
						return nil
					}
					attempt := attempts.Add(1)
					conn, err := pool.Acquire(ctx)
					if err != nil {
						return err
					}
					defer conn.Release()
					if attempt == 1 {
						pids <- conn.Conn().PgConn().PID()
					}
					err = pgx.BeginFunc(ctx, conn.Conn(), func(tx pgx.Tx) error {
						if _, err := tx.Exec(ctx, "UPDATE "+table+" SET connection=$2 WHERE id=$1", id.NodeID, connection); err != nil {
							return err
						}
						return ctx.Err()
					})
					if attempt == 1 {
						results <- err
					}
					return err
				},
				Disconnected: func(ctx context.Context, id Identity, connection string, _ uint64) {
					if id.NodeID != slow.NodeID {
						return
					}
					err := pgx.BeginTxFunc(ctx, pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
						var nodeID string
						if err := tx.QueryRow(ctx, "SELECT id FROM "+table+" WHERE id=$1 FOR UPDATE", id.NodeID).Scan(&nodeID); err != nil {
							return err
						}
						if _, err := tx.Exec(ctx, "UPDATE "+table+" SET connection=NULL WHERE id=$1 AND connection=$2", id.NodeID, connection); err != nil {
							return err
						}
						return ctx.Err()
					})
					if err != nil {
						t.Error(err)
					}
				},
			})
			server := httptest.NewServer(hub)
			defer server.Close()
			defer hub.Close()
			live := connectRawNode(t, server.URL, fast)
			defer live.Close()
			go serveRawInfo(live)
			start := time.Now()
			beginRawNode(t, server.URL, slow)
			var pid uint32
			select {
			case pid = <-pids:
			case <-time.After(time.Second):
				t.Fatal("database callback not entered")
			}
			wait(t, func() bool {
				var blocked bool
				err := pool.QueryRow(ctx, "SELECT COALESCE(wait_event_type='Lock',false) FROM pg_stat_activity WHERE pid=$1", pid).Scan(&blocked)
				return err == nil && blocked
			})
			assertInfoResponsive(t, hub, fast)
			assertDuplicateRejected(t, server.URL, slow.NodeID, "test")
			if mode == "close" {
				assertPrompt(t, hub.Close)
			}
			select {
			case err := <-results:
				want := context.DeadlineExceeded
				if mode == "close" {
					want = context.Canceled
				}
				if !errors.Is(err, want) {
					t.Fatalf("pgx cancellation=%v want=%v", err, want)
				}
			case <-time.After(callbackTimeout + time.Second):
				t.Fatal("pgx exceeded callback budget")
			}
			if mode == "deadline" {
				elapsed := time.Since(start)
				if elapsed < callbackTimeout || elapsed > callbackTimeout+time.Second {
					t.Fatalf("callback elapsed=%s", elapsed)
				}
				assertInfoResponsive(t, hub, fast)
			}
			if err := lock.Rollback(ctx); err != nil {
				t.Fatal(err)
			}
			wait(t, func() bool { return reservationReleased(hub, slow.NodeID) })
			var persisted *string
			if err := pool.QueryRow(ctx, "SELECT connection FROM "+table+" WHERE id=$1", slow.NodeID).Scan(&persisted); err != nil {
				t.Fatal(err)
			}
			if persisted != nil {
				t.Fatal("canceled opening left presence after cleanup")
			}
			if mode == "deadline" {
				reconnected := connectRawNode(t, server.URL, slow)
				defer reconnected.Close()
				wait(t, func() bool { return hub.Online(slow.NodeID) })
				hub.mu.Lock()
				current := hub.peers[slow.NodeID].id
				hub.mu.Unlock()
				if err := pool.QueryRow(ctx, "SELECT connection FROM "+table+" WHERE id=$1", slow.NodeID).Scan(&persisted); err != nil {
					t.Fatal(err)
				}
				if persisted == nil || *persisted != current {
					t.Fatal("old canceled write replaced new connection")
				}
				hub.Disconnect(slow.NodeID)
				wait(t, func() bool { return reservationReleased(hub, slow.NodeID) })
			}
			t.Logf("real PostgreSQL row lock: mode=%s callback canceled, unrelated RPC responsive, cleanup settled", mode)
		})
	}
}
