package execution

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/modelconfiguration"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimegateway"
	"github.com/google/uuid"
)

// closeCountingLease counts Close calls on the lease a Worker owns. It forwards
// to inner; without inner, any call other than Close fails the test.
type closeCountingLease struct {
	t        *testing.T
	inner    Ownership
	closable atomic.Bool
	closes   atomic.Int32
}

func (l *closeCountingLease) CheckOwnership(ctx context.Context) error {
	if l.inner == nil {
		l.t.Error("unexpected call to CheckOwnership")
		return errors.New("unexpected call to CheckOwnership")
	}
	return l.inner.CheckOwnership(ctx)
}

func (l *closeCountingLease) CancelOperations(ctx context.Context, cancel context.CancelFunc) error {
	if l.inner == nil {
		l.t.Error("unexpected call to CancelOperations")
		return errors.New("unexpected call to CancelOperations")
	}
	return l.inner.CancelOperations(ctx, cancel)
}

func (l *closeCountingLease) Close(ctx context.Context) error {
	l.closes.Add(1)
	if !l.closable.Load() {
		l.t.Error("lease closed before its owner finished")
	}
	if _, bounded := ctx.Deadline(); !bounded || ctx.Err() != nil {
		l.t.Error("lease closed without a live bounded context", ctx.Err())
	}
	if l.inner == nil {
		return nil
	}
	return l.inner.Close(ctx)
}

// unusedObserver fails the test on any observation. The Workers it serves run
// no Turn.
type unusedObserver struct{ t *testing.T }

func (o unusedObserver) ObserveDeploymentModelProvider(context.Context, modelconfiguration.Observation) (int64, error) {
	o.t.Error("unexpected call to ObserveDeploymentModelProvider")
	return 0, errors.New("unexpected call to ObserveDeploymentModelProvider")
}

func TestStartWorkerFailureClosesLeaseOnce(t *testing.T) {
	if _, err := StartWorker(t.Context(), &Dispatcher{}, Owner{}); err == nil {
		t.Fatal("worker started without an execution lease")
	}
	// The failed request's context is already canceled; the close must not inherit it.
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	for name, start := range map[string]func(*testing.T, *closeCountingLease) error{
		"negative concurrency": func(t *testing.T, lease *closeCountingLease) error {
			_, err := StartWorker(canceled, &Dispatcher{MaxConcurrentExecutions: -1}, Owner{Lease: lease})
			return err
		},
		"excess concurrency": func(t *testing.T, lease *closeCountingLease) error {
			_, err := StartWorker(canceled, &Dispatcher{MaxConcurrentExecutions: 1025}, Owner{Lease: lease})
			return err
		},
		"missing Credentials": func(t *testing.T, lease *closeCountingLease) error {
			_, err := StartWorker(canceled, &Dispatcher{Observer: unusedObserver{t}}, Owner{Lease: lease})
			return err
		},
		"missing observer": func(t *testing.T, lease *closeCountingLease) error {
			_, err := StartWorker(canceled, &Dispatcher{Credentials: &recordingCredentials{}}, Owner{Lease: lease})
			return err
		},
		"missing Store": func(t *testing.T, lease *closeCountingLease) error {
			_, err := StartWorker(canceled, &Dispatcher{Credentials: &recordingCredentials{}, Observer: unusedObserver{t}}, Owner{Lease: lease})
			return err
		},
		"deployment claim": func(t *testing.T, lease *closeCountingLease) error {
			s, owner := resetManagerStore(t)
			lease.inner = owner.Lease
			id := uuid.NewString()
			dispatcher := &Dispatcher{Store: s, Registry: runtimegateway.NewRegistry(), Credentials: &recordingCredentials{}, Observer: unusedObserver{t}, ManagedRuntimes: NewDeferredRuntimeProvider(id, func(context.Context) (*RuntimeProvider, error) { return nil, nil })}
			_, err := StartWorker(canceled, dispatcher, Owner{Lease: lease, Store: owner.Store})
			if ping := owner.Lease.CheckOwnership(t.Context()); !errors.Is(ping, pgunit.ErrLeaseClosed) {
				t.Error("failed start kept the database lease", ping)
			}
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			lease := &closeCountingLease{t: t}
			lease.closable.Store(true)
			if err := start(t, lease); err == nil {
				t.Fatal("worker started")
			}
			if closes := lease.closes.Load(); closes != 1 {
				t.Fatal("failed start closed the lease", closes, "times")
			}
		})
	}
}

func TestWorkerRunClosesLeaseAfterDrain(t *testing.T) {
	s, owner := resetManagerStore(t)
	lease := &closeCountingLease{t: t, inner: owner.Lease}
	id := uuid.NewString()
	dispatcher := &Dispatcher{Store: s, Registry: runtimegateway.NewRegistry(), Credentials: &recordingCredentials{}, Observer: unusedObserver{t}, ManagedRuntimes: NewDeferredRuntimeProvider(id, func(context.Context) (*RuntimeProvider, error) { return nil, nil })}
	worker, err := StartWorker(t.Context(), dispatcher, Owner{Lease: lease, Store: owner.Store})
	if err != nil {
		t.Fatal(err)
	}
	// An external provisioning caller is still in flight when Run exits.
	worker.runtimes.active.Add(1)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	cancel()
	select {
	case <-worker.runtimes.ctx.Done():
	case <-time.After(5 * time.Second):
		worker.runtimes.active.Done()
		t.Fatal("Run did not stop its runtimes")
	}
	lease.closable.Store(true)
	worker.runtimes.active.Done()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not exit after draining")
	}
	if closes := lease.closes.Load(); closes != 1 {
		t.Fatal("Run closed the lease", closes, "times")
	}
	if ping := owner.Lease.CheckOwnership(t.Context()); !errors.Is(ping, pgunit.ErrLeaseClosed) {
		t.Fatal("Run kept the database lease", ping)
	}
}
