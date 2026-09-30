package pgunit

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgtest"
)

func TestLeaseCloseWaitsForCancelledConnectionCleanup(t *testing.T) {
	observer := pgtest.Open(t)
	var armed atomic.Bool
	blocked, release := make(chan struct{}), make(chan struct{})
	var signal, unblocked sync.Once
	unblock := func() { unblocked.Do(func() { close(release) }) }
	pool := pgtest.OpenIsolated(t, func(cfg *pgxpool.Config) {
		dial := cfg.ConnConfig.DialFunc
		cfg.ConnConfig.DialFunc = func(ctx context.Context, network, address string) (net.Conn, error) {
			if armed.Load() {
				signal.Do(func() { close(blocked) })
				select {
				case <-release:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
			return dial(ctx, network, address)
		}
	})
	lease, err := AcquireLease(t.Context(), pool)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		unblock()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := lease.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	connection := lease.conn.Conn().PgConn()
	armed.Store(true)
	queryCtx, cancelQuery := context.WithCancel(t.Context())
	defer cancelQuery()
	queryDone := make(chan error, 1)
	go func() {
		queryDone <- lease.Transaction(queryCtx, func(ctx context.Context, tx pgx.Tx) error {
			_, err := tx.Exec(ctx, "SELECT pg_sleep(10)")
			return err
		})
	}()
	deadline := time.Now().Add(2 * time.Second)
	for {
		var sleeping bool
		err := observer.QueryRow(t.Context(), "SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE pid=$1 AND wait_event='PgSleep')", connection.PID()).Scan(&sleeping)
		if err != nil {
			t.Fatal(err)
		}
		if sleeping {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("owner query did not reach PostgreSQL")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancelQuery()
	if err := <-queryDone; err == nil || !connection.IsClosed() {
		t.Fatal("cancelled query did not close the driver connection", err)
	}
	select {
	case <-blocked:
	case <-time.After(2 * time.Second):
		t.Fatal("asynchronous cancellation did not reach the dial gate")
	}
	closeCtx, cancelClose := context.WithTimeout(t.Context(), 25*time.Millisecond)
	err = lease.Close(closeCtx)
	cancelClose()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("Close returned before blocked cleanup or ignored its deadline", err)
	}
	if err := lease.CheckOwnership(t.Context()); err == nil {
		t.Fatal("timed-out cleanup restored writer authority")
	}
	closed := make(chan error, 1)
	go func() { closed <- lease.Close(t.Context()) }()
	select {
	case err := <-closed:
		t.Fatal("repeated Close forgot pending cleanup", err)
	case <-time.After(25 * time.Millisecond):
	}
	unblock()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not finish after cleanup resumed")
	}
	select {
	case <-connection.CleanupDone():
	default:
		t.Fatal("successful Close left driver cleanup pending")
	}
}
