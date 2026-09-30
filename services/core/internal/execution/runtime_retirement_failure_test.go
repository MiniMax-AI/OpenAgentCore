package execution

import (
	"context"
	"errors"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Each case has its own real advisory-lock namespace. The delayed driver read
// lets the cancellation fence hit its own deadline without shortening production
// timeouts or depending on an unrelated SQL failure to exercise this branch.
func retirementFailureLease(t *testing.T, armed *atomic.Bool, reading chan struct{}, release <-chan struct{}) (*store.ExecutionLease, *pgxpool.Pool) {
	t.Helper()
	dsn := os.Getenv("OAC_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("dedicated PostgreSQL required")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(cfg.ConnConfig.Database, "oac_") || !strings.HasSuffix(cfg.ConnConfig.Database, "_tests") {
		t.Fatal("dedicated test database required")
	}
	admin, err := pgxpool.NewWithConfig(t.Context(), cfg.Copy())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	name := "oac_retirement_" + uuid.NewString()[:8] + "_tests"
	quoted := pgx.Identifier{name}.Sanitize()
	if _, err := admin.Exec(t.Context(), "CREATE DATABASE "+quoted); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := admin.Exec(ctx, "DROP DATABASE "+quoted+" WITH (FORCE)"); err != nil {
			t.Error(err)
		}
	})
	cfg.ConnConfig.Database = name
	dial := cfg.ConnConfig.DialFunc
	cfg.ConnConfig.DialFunc = func(ctx context.Context, network, address string) (net.Conn, error) {
		c, err := dial(ctx, network, address)
		if err != nil {
			return nil, err
		}
		return &delayedLeaseRead{Conn: c, armed: armed, reading: reading, release: release}, nil
	}
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	lease, err := store.New(pool).AcquireExecutionLease(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := lease.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	return lease, pool
}

func TestFailedInventoryRetirementClosesAdmissionAndRetainsGate(t *testing.T) {
	for _, mode := range []string{"gate_timeout", "lease_loss"} {
		t.Run(mode, func(t *testing.T) {
			var armed atomic.Bool
			reading, releaseRead := make(chan struct{}), make(chan struct{})
			var readOnce sync.Once
			unblockRead := func() { readOnce.Do(func() { close(releaseRead) }) }
			defer unblockRead()
			lease, pool := retirementFailureLease(t, &armed, reading, releaseRead)
			m := testRuntimeManager(t)
			m.store = lease.Store()
			m.loadDeployment = func(context.Context) (*RuntimeProvider, error) { return nil, nil }
			m.mutationGate = make(chan struct{}, 1)
			// This fixture models an already loaded node deployment; its provider is
			// needed only for node admission, not for external sandbox operations.
			m.config.Provider = &drainFixtureProvider{}
			original, err := m.node("retiring")
			if err != nil {
				t.Fatal(err)
			}
			operation, finish, err := original.lifecycle.beginReconcile(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			releaseProvider := make(chan struct{})
			var providerOnce sync.Once
			unblockProvider := func() { providerOnce.Do(func() { close(releaseProvider) }) }
			t.Cleanup(unblockProvider)
			m.active.Add(1)
			go func() { defer m.active.Done(); defer finish(); <-operation.Done(); <-releaseProvider }()
			var queryDone chan error
			if mode == "gate_timeout" {
				// This independent owner caller has its own bounded query context. Unlike
				// a lifecycle scan, it is not canceled by the manager's shutdown context.
				queryCtx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
				defer cancel()
				queryDone = make(chan error, 1)
				armed.Store(true)
				go func() { queryDone <- lease.Ping(queryCtx) }()
				select {
				case <-reading:
				case <-time.After(2 * time.Second):
					t.Fatal("owner query did not reach delayed read")
				}
				defer func() {
					if queryDone != nil {
						unblockRead()
						select {
						case <-queryDone:
						case <-time.After(2 * time.Second):
							t.Error("owned query did not settle")
						}
					}
				}()
			} else {
				var killed bool
				err := pool.QueryRow(t.Context(), `SELECT pg_terminate_backend(pid,1000) FROM pg_locks WHERE locktype='advisory' AND database=(SELECT oid FROM pg_database WHERE datname=current_database())`).Scan(&killed)
				if err != nil || !killed {
					t.Fatal("could not terminate this test's owner", killed, err)
				}
			}
			_, err = m.applyInventory(m.snapshotNodes(), nil)
			if err == nil {
				t.Fatal("failed owner accepted inventory retirement")
			}
			if mode == "gate_timeout" && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal("did not exercise cancellation gate timeout", err)
			}
			if _, finish, err := m.enter(t.Context()); err == nil {
				finish()
				t.Error("manual caller admitted before coordinator consumed failure")
			}
			if _, err := m.node("retiring"); err == nil {
				t.Error("failed retirement admitted a replacement gate")
			}
			if _, err := m.node("new-node"); err == nil {
				t.Error("failed retirement admitted another lifecycle")
			}
			if unlock, err := m.lockMutation(t.Context()); err == nil {
				unlock()
				t.Error("failed retirement admitted a configuration mutation")
			}
			m.mu.Lock()
			retained := m.nodes["retiring"] == original && original.retiring
			m.mu.Unlock()
			if !retained {
				t.Error("failed retirement discarded its original lifecycle")
			}
			if original.lifecycle.ctx.Err() != nil {
				t.Error("failed fence claimed successful lifecycle cancellation")
			}
			select {
			case failure := <-m.failed:
				if failure == nil {
					t.Error("empty owner failure")
				}
			default:
				t.Error("owner failure was not published")
			}
			if queryDone != nil {
				unblockRead()
				select {
				case err := <-queryDone:
					if err != nil {
						t.Fatal("fence timeout damaged unrelated owner read", err)
					}
				case <-time.After(2 * time.Second):
					t.Fatal("owner read did not settle")
				}
				queryDone = nil
			}
			// Shutdown can now cancel the original work, but cannot call it settled
			// merely because cancellation was requested or a serial gate became free.
			m.stop()
			drained := make(chan struct{})
			go func() { m.drain(); close(drained) }()
			select {
			case <-drained:
				t.Fatal("shutdown skipped retained provider settlement")
			case <-time.After(25 * time.Millisecond):
			}
			unblockProvider()
			select {
			case <-drained:
			case <-time.After(2 * time.Second):
				t.Fatal("shutdown retirement accounting did not settle")
			}
			m.mu.Lock()
			retained = m.nodes["retiring"] == original
			m.mu.Unlock()
			if !retained {
				t.Fatal("failed-retirement entry was removed by a task that never canceled it")
			}
		})
	}
}

// No provider method is called by the retirement ownership fixture.
type drainFixtureProvider struct{ sandbox.SandboxProvider }

// Done is evaluated only after lockMutation's initial admission check, letting
// this test close admission while a request is already waiting for its gate.
type mutationWaitContext struct {
	context.Context
	waiting chan struct{}
	once    sync.Once
}

func (c *mutationWaitContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.waiting) })
	return c.Context.Done()
}

func TestFailedManagerRejectsAlreadyWaitingMutation(t *testing.T) {
	m := testRuntimeManager(t)
	m.loadDeployment = func(context.Context) (*RuntimeProvider, error) { return nil, nil }
	m.mutationGate = make(chan struct{}, 1)
	m.mutationGate <- struct{}{}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	waiting := &mutationWaitContext{Context: ctx, waiting: make(chan struct{})}
	result := make(chan error, 1)
	go func() {
		unlock, err := m.lockMutation(waiting)
		if unlock != nil {
			unlock()
		}
		result <- err
	}()
	select {
	case <-waiting.waiting:
	case <-time.After(time.Second):
		t.Fatal("mutation never reached gate wait")
	}
	m.mu.Lock()
	m.closed = true
	m.mu.Unlock()
	<-m.mutationGate
	select {
	case err := <-result:
		if !errors.Is(err, ErrExecutionUnavailable) {
			t.Fatal("waiting mutation bypassed synchronous admission closure", err)
		}
	case <-time.After(time.Second):
		t.Fatal("waiting mutation did not settle")
	}
}
