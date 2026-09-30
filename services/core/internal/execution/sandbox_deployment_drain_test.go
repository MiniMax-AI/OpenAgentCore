package execution

import (
	"bytes"
	"context"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgtest"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimegateway"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/docker"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/node"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// delayedLeaseRead holds one real PostgreSQL response while the deployment drain
// begins. Cancellation during this driver read closes the advisory-lock owner.
type delayedLeaseRead struct {
	net.Conn
	armed   *atomic.Bool
	held    atomic.Bool
	reading chan struct{}
	release <-chan struct{}
}

func (c *delayedLeaseRead) Write(p []byte) (int, error) {
	if bytes.Contains(p, []byte("-- ping")) && c.armed.CompareAndSwap(true, false) {
		c.held.Store(true)
	}
	return c.Conn.Write(p)
}
func (c *delayedLeaseRead) Read(p []byte) (int, error) {
	if c.held.CompareAndSwap(true, false) {
		close(c.reading)
		<-c.release
	}
	return c.Conn.Read(p)
}

// delayedReadWriter owns an isolated database, so each case has its own
// advisory-lock namespace. The delayed driver read lets a cancellation fence hit
// its own deadline without shortening production timeouts.
func delayedReadWriter(t *testing.T, armed *atomic.Bool, reading chan struct{}, release <-chan struct{}) (Owner, *deployment.Service, deployment.Reader, *pgxpool.Pool) {
	t.Helper()
	pool := pgtest.OpenIsolated(t, func(cfg *pgxpool.Config) {
		dial := cfg.ConnConfig.DialFunc
		cfg.ConnConfig.DialFunc = func(ctx context.Context, network, address string) (net.Conn, error) {
			c, err := dial(ctx, network, address)
			if err != nil {
				return nil, err
			}
			return &delayedLeaseRead{Conn: c, armed: armed, reading: reading, release: release}, nil
		}
	})
	lease, err := pgunit.AcquireLease(t.Context(), pool)
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
	deployments, reader, operations := testDeployment(t, pool, nil, lease)
	return Owner{Lease: lease, Store: store.NewExecution(store.New(pool), lease), Deployment: operations, Sessions: sessionExecution(t, lease)}, deployments, reader, pool
}

func TestSandboxDeploymentDrainPreservesLeaseInFlightRead(t *testing.T) {
	for _, mode := range []string{"deployment", "inventory", "manual"} {
		t.Run(mode, func(t *testing.T) {
			testLifecycleCancellationPreservesLease(t, mode)
		})
	}
}

func testLifecycleCancellationPreservesLease(t *testing.T, mode string) {
	var armed atomic.Bool
	reading, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	owner, deployments, reader, _ := delayedReadWriter(t, &armed, reading, release)
	hub := node.NewHub(node.HubOptions{})
	defer hub.Close()
	id := uuid.NewString()
	configuration := &RuntimeProvider{InstallationID: id, ProviderKind: "docker", Mode: "nodes", Generation: 1, CoreURL: "https://core.example/api/v1", BackendFingerprint: strings.Repeat("a", 64), Provider: hub.Proxy(uuid.NewString(), "docker", docker.Operations(), 1)}
	m, err := newRuntimeManager(owner, deployments, reader, runtimegateway.NewRegistry(), NewDeferredRuntimeProvider(id, func(context.Context) (*RuntimeProvider, error) { return configuration, nil }))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { unblock(); m.stop(); m.drain() }()
	if _, err := m.ensureDeployment(t.Context()); err != nil {
		t.Fatal(err)
	}
	n, err := m.node(uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	var delayed *delayedCancellationContext
	if mode == "manual" {
		delayed = &delayedCancellationContext{Context: n.lifecycle.ctx, release: make(chan struct{}), finished: make(chan struct{})}
		n.lifecycle.ctx = delayed
		defer delayed.unblock()
	}
	m.active.Add(1)
	queryDone := make(chan error, 1)
	leaseFree := make(chan error, 1)
	armed.Store(true)
	go func() {
		defer m.active.Done()
		parent := n.lifecycle.ctx
		if mode == "manual" {
			parent = t.Context()
		}
		ctx, finish, err := n.lifecycle.beginReconcile(parent)
		if err != nil {
			queryDone <- err
			return
		}
		defer finish()
		queryDone <- owner.Lease.CheckOwnership(ctx)
		<-ctx.Done()
		// Provider settlement remains outside the cancellation fence. A fresh owner
		// read must proceed even before this tracked lifecycle operation returns.
		leaseFree <- owner.Lease.CheckOwnership(t.Context())
	}()
	select {
	case <-reading:
	case <-time.After(2 * time.Second):
		t.Fatal("lease query did not reach its driver read")
	}
	paused := make(chan error, 1)
	go func() {
		if mode == "inventory" {
			_, err := m.applyInventory(map[string]*runtimeNode{n.lifecycle.nodeID: n}, nil)
			paused <- err
		} else {
			paused <- m.pauseDeployment(t.Context())
		}
	}()
	// The cancellation is only legal after the leased query has returned. The
	// small observation window is not a delay used to make the operation succeed.
	select {
	case <-n.lifecycle.ctx.Done():
		t.Log("drain cancelled a live leased operation")
	case <-time.After(100 * time.Millisecond):
	}
	unblock()
	queryErr := <-queryDone
	select {
	case err := <-leaseFree:
		if err != nil {
			t.Fatal("cancellation held or lost the lease during provider settlement", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("lifecycle cancellation or provider settlement did not proceed beyond the fence")
	}
	select {
	case err := <-paused:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancellation depended on delayed AfterFunc or drain did not finish")
	}
	if mode == "inventory" {
		settled := make(chan struct{})
		go func() { m.active.Wait(); close(settled) }()
		select {
		case <-settled:
		case <-time.After(2 * time.Second):
			t.Fatal("retired lifecycle did not settle")
		}
	}
	if delayed != nil {
		delayed.unblock()
	}
	if err := owner.Lease.CheckOwnership(t.Context()); err != nil {
		t.Fatalf("deployment drain destroyed the owner connection (in-flight query: %v): %v", queryErr, err)
	}
	if queryErr != nil {
		t.Fatal("drain interrupted the leased read", queryErr)
	}
}

// Force the manual-reconcile AfterFunc to run after the synchronous fence. Value
// intentionally hides cancelCtx internals so context uses this AfterFunc method.
type delayedCancellationContext struct {
	context.Context
	release     chan struct{}
	finished    chan struct{}
	releaseOnce sync.Once
	finishOnce  sync.Once
}

func (c *delayedCancellationContext) Value(any) any { return nil }
func (c *delayedCancellationContext) AfterFunc(f func()) func() bool {
	stop := context.AfterFunc(c.Context, func() {
		<-c.release
		f()
		c.finishOnce.Do(func() { close(c.finished) })
	})
	return func() bool {
		stopped := stop()
		if stopped {
			c.finishOnce.Do(func() { close(c.finished) })
		}
		return stopped
	}
}
func (c *delayedCancellationContext) unblock() {
	c.releaseOnce.Do(func() { close(c.release) })
	select {
	case <-c.finished:
	case <-time.After(time.Second):
	}
}

func TestSandboxDeploymentDrainFailureCannotReactivate(t *testing.T) {
	_, owner, deployments, reader := resetManagerStore(t)
	hub := node.NewHub(node.HubOptions{})
	defer hub.Close()
	id := uuid.NewString()
	configuration := &RuntimeProvider{InstallationID: id, ProviderKind: "docker", Mode: "nodes", Generation: 1, CoreURL: "https://core.example/api/v1", BackendFingerprint: strings.Repeat("a", 64), Provider: hub.Proxy(uuid.NewString(), "docker", docker.Operations(), 1)}
	m, err := newRuntimeManager(owner, deployments, reader, runtimegateway.NewRegistry(), NewDeferredRuntimeProvider(id, func(context.Context) (*RuntimeProvider, error) { return configuration, nil }))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { m.stop(); m.drain() }()
	if _, err := m.ensureDeployment(t.Context()); err != nil {
		t.Fatal(err)
	}
	n, err := m.node(uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if err := owner.Lease.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	first := m.pauseDeployment(t.Context())
	if first == nil {
		t.Fatal("drain accepted lost ownership")
	}
	if n.lifecycle.ctx.Err() != nil {
		t.Fatal("failed fence canceled outside the lease gate")
	}
	if err := m.pauseDeployment(t.Context()); err == nil {
		t.Fatal("repeated drain forgot its failure")
	}
	if err := m.activateDeployment(t.Context(), deployment.View{InstallationID: id, Generation: 1, Mode: "nodes", Provider: "docker"}); err == nil {
		t.Fatal("failed drain reopened the provider")
	}
	if _, _, err := m.enter(t.Context()); err == nil {
		t.Fatal("failed drain admitted new work")
	}
	select {
	case err := <-m.failed:
		if err == nil {
			t.Fatal("missing owner failure")
		}
	default:
		t.Fatal("failed fence did not stop the owner")
	}
}
