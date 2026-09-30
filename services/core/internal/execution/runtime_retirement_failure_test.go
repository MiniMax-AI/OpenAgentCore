package execution

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

// retirementFenceLease bounds only the cancellation attempt. The concurrent
// owner query retains the production lease deadline and must remain usable.
type retirementFenceLease struct{ Ownership }

func (l retirementFenceLease) CancelOperations(ctx context.Context, cancel context.CancelFunc) error {
	ctx, stop := context.WithTimeout(ctx, 100*time.Millisecond)
	defer stop()
	return l.Ownership.CancelOperations(ctx, cancel)
}

func TestFailedInventoryRetirementClosesAdmissionAndRetainsGate(t *testing.T) {
	for _, mode := range []string{"gate_timeout", "lease_loss"} {
		t.Run(mode, func(t *testing.T) {
			var armed atomic.Bool
			reading, releaseRead := make(chan struct{}), make(chan struct{})
			var readOnce sync.Once
			unblockRead := func() { readOnce.Do(func() { close(releaseRead) }) }
			defer unblockRead()
			owner, _, pool := delayedReadWriter(t, &armed, reading, releaseRead)
			m := testRuntimeManager(t)
			m.store, m.lease = owner.Store, owner.Lease
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
				// Both lease operations have the same production deadline. Give only
				// the later fence a shorter caller deadline so it expires first.
				m.lease = retirementFenceLease{Ownership: owner.Lease}
				queryDone = make(chan error, 1)
				armed.Store(true)
				go func() { queryDone <- owner.Lease.CheckOwnership(t.Context()) }()
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
					queryDone = nil
					if err != nil {
						t.Fatal("fence timeout damaged unrelated owner read", err)
					}
				case <-time.After(2 * time.Second):
					t.Fatal("owner read did not settle")
				}
				if err := owner.Lease.CheckOwnership(t.Context()); err != nil {
					t.Fatal("fence timeout lost the execution lease", err)
				}
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
