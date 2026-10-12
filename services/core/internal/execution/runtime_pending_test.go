package execution

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimegateway"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/gorilla/websocket"
)

func TestPendingProvisionRefillsAtCursorEnd(t *testing.T) {
	for _, cursor := range []string{"", "previous"} {
		t.Run(cursor, func(t *testing.T) {
			var queries []string
			r := &runtimeLifecycle{pendingCursor: cursor, reader: &strictDeploymentReader{t: t, unallocatedEnvironments: func(_ context.Context, _, after string) ([]deployment.UnallocatedEnvironment, error) {
				queries = append(queries, after)
				return nil, nil
			}}}
			if err := r.provisionPending(t.Context()); err != nil {
				t.Fatal(err)
			}
			want := []string{""}
			if cursor != "" {
				want = []string{cursor, ""}
			}
			if !reflect.DeepEqual(queries, want) || r.pendingCursor != "" {
				t.Fatal("pending scan wasted its EOF cycle or refilled repeatedly", queries, r.pendingCursor)
			}
		})
	}
}

func TestPendingProvisionProcessesNewIDBeforeOldCursor(t *testing.T) {
	p := &retentionProvider{}
	f := newWorkspaceSettlementFixture(t, p)
	r := f.lifecycle
	r.pendingCursor = "ffffffff-ffff-ffff-ffff-ffffffffffff"
	if err := r.provisionPending(t.Context()); err != nil {
		t.Fatal(err)
	}
	got, err := r.reader.EnvironmentAllocation(t.Context(), deployment.AllocationKey{TenantID: f.session.TenantID, EnvironmentID: f.session.Environment.ID})
	if err != nil || got.State != "running" || p.creates != 1 {
		t.Fatal("new committed Environment was stranded at EOF", got, err, p.creates)
	}
}

type connectedBootstrapProvider struct {
	sandbox.SandboxProvider
	connect func(sandbox.Bootstrap)
}

func (p *connectedBootstrapProvider) Create(_ context.Context, b sandbox.Bootstrap) (sandbox.Info, error) {
	p.connect(b)
	return sandbox.Info{Reference: b.Reference, ProviderID: b.AllocationID, State: "running", BootstrapComplete: true, CreateSettled: true}, nil
}
func TestProvisionPublishesExistingAuthenticatedConnection(t *testing.T) {
	p := &connectedBootstrapProvider{}
	f := newWorkspaceSettlementFixture(t, p)
	r := f.lifecycle
	r.connections = map[string]*runtimeConnection{}
	before, err := r.sessions.GetEnvironment(t.Context(), f.session.TenantID, f.session.Environment.ID)
	if err != nil {
		t.Fatal(err)
	}
	handler := runtimegateway.NewHandler(runtimegateway.HandlerConfig{Authenticator: runtimegateway.NewAuthenticator(r.sessions), Registry: r.registry})
	server := httptest.NewServer(http.HandlerFunc(handler.WS))
	defer server.Close()
	p.connect = func(b sandbox.Bootstrap) {
		query := url.Values{"device_id": {b.DeviceID}, "version": {proto.Version}}
		conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"?"+query.Encode(), http.Header{"Authorization": {"Bearer " + b.Credential}})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = conn.Close() })
		if _, err = r.registry.WaitForDevice(t.Context(), b.DeviceID, time.Second); err != nil {
			t.Fatal(err)
		}
	}
	owner, err := r.provision(t.Context(), f.session.TenantID, f.session.Environment.ID, r.config.InstallationID)
	if err != nil {
		t.Fatal(err)
	}
	if current := r.connections[owner.EnvironmentID]; current == nil || !current.connected {
		t.Fatal("Create finished without publishing authenticated connection")
	}
	// Transport connection is not permission to skip initialization.
	env, err := r.sessions.GetEnvironment(t.Context(), owner.TenantID, owner.EnvironmentID)
	if err != nil || env.Initialization != before.Initialization {
		t.Fatal("connection fabricated initialization", env, err)
	}
}

func TestCommittedProvisionSharesLifecycleCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	entered := make(chan struct{})
	r := &runtimeLifecycle{ctx: ctx, stop: cancel, gate: make(chan struct{}, 1), reader: &strictDeploymentReader{t: t, unallocatedEnvironments: func(ctx context.Context, _, _ string) ([]deployment.UnallocatedEnvironment, error) {
		close(entered)
		<-ctx.Done()
		return nil, ctx.Err()
	}}}
	done := make(chan error, 1)
	go func() { done <- r.provisionCommitted(t.Context()) }()
	<-entered
	r.cancelOperations()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("pending provisioning escaped lifecycle cancellation")
	}
	if len(r.gate) != 0 {
		t.Fatal("pending provisioning retained lifecycle gate")
	}
}

func TestDirectDeploymentNotifiesPendingLane(t *testing.T) {
	m := testRuntimeManager(t)
	m.config.Mode = string(sandbox.DeploymentDirect)
	for range 10 {
		if err := m.reservePlacements(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	n := m.nodes[""]
	if n == nil || len(n.lifecycle.pendingWake) != 1 || len(n.lifecycle.wakeHints) != 0 {
		t.Fatal("direct demand did not coalesce onto its pending lifecycle lane")
	}
}

type pendingAttemptReader struct {
	sessions.Reader
	attempted []string
}

func (s *pendingAttemptReader) GetEnvironment(_ context.Context, _, id string) (sessions.Environment, error) {
	s.attempted = append(s.attempted, id)
	return sessions.Environment{}, sessions.ErrNotFound
}
func TestCommittedProvisionIncludesIDsBeforeAndAfterMaintenanceCursor(t *testing.T) {
	attempts := &pendingAttemptReader{}
	r := &runtimeLifecycle{ctx: t.Context(), gate: make(chan struct{}, 1), pendingCursor: "m", sessions: attempts, lease: heldLease{}, reader: &strictDeploymentReader{t: t, unallocatedEnvironments: func(_ context.Context, _, after string) ([]deployment.UnallocatedEnvironment, error) {
		candidates := []deployment.UnallocatedEnvironment{{ID: "a"}, {ID: "z"}}
		if after != "" {
			return candidates[1:], nil
		}
		return candidates, nil
	}}}
	if err := r.provisionCommitted(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(attempts.attempted, []string{"a", "z"}) {
		t.Fatal("fresh placement below cursor waited for maintenance", attempts.attempted)
	}
}

func TestLateRegistrationPublishesWithoutMaintenanceTick(t *testing.T) {
	var bootstrap sandbox.Bootstrap
	p := &connectedBootstrapProvider{connect: func(b sandbox.Bootstrap) { bootstrap = b }}
	f := newWorkspaceSettlementFixture(t, p)
	r := f.lifecycle
	r.ctx = t.Context()
	r.gate = make(chan struct{}, 1)
	r.connectionWake = make(chan *runtimegateway.Session, 1)
	r.connections = map[string]*runtimeConnection{}
	owner, err := r.provision(t.Context(), f.session.TenantID, f.session.Environment.ID, r.config.InstallationID)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.registry.PendingWaiters(owner.DeviceID)) != 1 {
		t.Fatal("late Runtime has no registration watch")
	}
	handler := runtimegateway.NewHandler(runtimegateway.HandlerConfig{Authenticator: runtimegateway.NewAuthenticator(r.sessions), Registry: r.registry})
	server := httptest.NewServer(http.HandlerFunc(handler.WS))
	defer server.Close()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	started := make(chan struct{})
	go func() {
		done <- runRuntimeMaintenance(ctx, nil, nil, func(context.Context) error { close(started); return nil }, nil, nil, r.connectionWake, r.observeAwaitingConnections)
	}()
	defer func() {
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Error(err)
		}
	}()
	<-started
	query := url.Values{"device_id": {bootstrap.DeviceID}, "version": {proto.Version}}
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"?"+query.Encode(), http.Header{"Authorization": {"Bearer " + bootstrap.Credential}})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	deadline := time.Now().Add(time.Second)
	for {
		env, err := r.sessions.GetEnvironment(t.Context(), owner.TenantID, owner.EnvironmentID)
		if err != nil {
			t.Fatal(err)
		}
		if env.Status == "connected" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("late registration waited for absent maintenance ticker", env.Status)
		}
		time.Sleep(time.Millisecond)
	}
	if len(r.registry.PendingWaiters(owner.DeviceID)) != 0 {
		t.Fatal("connected Runtime retained watch")
	}
}

func TestAwaitingConnectionPassDropsStaleAndCoalescedWatches(t *testing.T) {
	for _, change := range []string{"deleted", "expired", "unsettled", "cleanup", "suspended", "node", "provider", "device", "missing"} {
		t.Run(change, func(t *testing.T) {
			owner := deployment.Allocation{DeviceID: "device", NodeID: "node", ProviderKey: "provider", State: "running", CreateSettled: true, ComputePhase: "running"}
			switch change {
			case "deleted":
				owner.SessionDeleted = true
			case "expired":
				owner.Expired = true
			case "unsettled":
				owner.CreateSettled = false
			case "cleanup":
				owner.State = "cleanup_pending"
			case "suspended":
				owner.ComputePhase = "suspended"
			case "node":
				owner.NodeID = "other"
			case "provider":
				owner.ProviderKey = "other"
			case "device":
				owner.DeviceID = "other"
			}
			r := &runtimeLifecycle{ctx: t.Context(), gate: make(chan struct{}, 1), nodeID: "node", config: RuntimeProvider{InstallationID: "provider"}, registry: runtimegateway.NewRegistry(), connectionWake: make(chan *runtimegateway.Session, 1), awaiting: map[string]deployment.AllocationKey{"device": {}, "another": {}}, reader: &strictDeploymentReader{t: t, environmentAllocation: func(context.Context, deployment.AllocationKey) (deployment.Allocation, error) {
				if change == "missing" {
					return deployment.Allocation{}, deployment.ErrNotFound
				}
				return owner, nil
			}}}
			for device := range r.awaiting {
				r.registry.WatchDevice(device, r.connectionWake)
			}
			if err := r.observeAwaitingConnections(t.Context()); err != nil {
				t.Fatal(err)
			}
			if len(r.awaiting) != 0 || len(r.registry.PendingWaiters("device")) != 0 || len(r.registry.PendingWaiters("another")) != 0 {
				t.Fatal("stale registration watch survived")
			}
		})
	}
}

func TestRuntimeCleanupAndRetirementReleaseConnectionWatches(t *testing.T) {
	m := testRuntimeManager(t)
	m.registry = runtimegateway.NewRegistry()
	n, err := m.node("node")
	if err != nil {
		t.Fatal(err)
	}
	r := n.lifecycle
	r.awaiting = map[string]deployment.AllocationKey{"first": {}, "second": {}}
	for device := range r.awaiting {
		r.registry.WatchDevice(device, r.connectionWake)
	}
	r.clearRuntimeState(deployment.Allocation{DeviceID: "first"})
	if len(r.registry.PendingWaiters("first")) != 0 || len(r.registry.PendingWaiters("second")) != 1 {
		t.Fatal("cleanup removed wrong watches")
	}
	if err := m.cancelLifecycles([]*runtimeNode{n}); err != nil {
		t.Fatal(err)
	}
	m.active.Add(1)
	m.retire(n)
	if len(r.registry.PendingWaiters("second")) != 0 || len(r.awaiting) != 0 {
		t.Fatal("retired lane retained watches")
	}
}

func TestAwaitingConnectionRejectsUnauthorizedOrClosedPeerWithoutRearming(t *testing.T) {
	for _, state := range []string{"unauthorized", "closed"} {
		t.Run(state, func(t *testing.T) {
			var bootstrap sandbox.Bootstrap
			p := &connectedBootstrapProvider{connect: func(b sandbox.Bootstrap) { bootstrap = b }}
			f := newWorkspaceSettlementFixture(t, p)
			r := f.lifecycle
			r.ctx = t.Context()
			r.gate = make(chan struct{}, 1)
			r.connectionWake = make(chan *runtimegateway.Session, 1)
			r.connections = map[string]*runtimeConnection{}
			owner, err := r.provision(t.Context(), f.session.TenantID, f.session.Environment.ID, r.config.InstallationID)
			if err != nil {
				t.Fatal(err)
			}
			handler := runtimegateway.NewHandler(runtimegateway.HandlerConfig{Authenticator: runtimegateway.NewAuthenticator(r.sessions), Registry: r.registry})
			server := httptest.NewServer(http.HandlerFunc(handler.WS))
			defer server.Close()
			query := url.Values{"device_id": {bootstrap.DeviceID}, "version": {proto.Version}}
			conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"?"+query.Encode(), http.Header{"Authorization": {"Bearer " + bootstrap.Credential}})
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			var peer *runtimegateway.Session
			select {
			case peer = <-r.connectionWake:
			case <-time.After(time.Second):
				t.Fatal("registration did not notify")
			}
			if state == "unauthorized" {
				if _, err = f.pool.Exec(t.Context(), `UPDATE devices SET credential_hash=$2 WHERE id=$1`, owner.DeviceID, strings.Repeat("a", 64)); err != nil {
					t.Fatal(err)
				}
			} else {
				peer.Close("test closed peer")
				// Model a stale registered pointer; a closed peer is never a fresh absence.
				r.registry.Register(peer)
			}
			if err = r.observeAwaitingConnections(t.Context()); err != nil {
				t.Fatal(err)
			}
			if len(r.awaiting) != 0 || len(r.connectionWake) != 0 || len(r.registry.PendingWaiters(owner.DeviceID)) != 0 {
				t.Fatal("invalid peer rearmed an immediate notification loop")
			}
			if current := r.connections[owner.EnvironmentID]; current != nil && current.connected {
				t.Fatal("invalid peer published connected")
			}
		})
	}
}

func TestManagerStopDrainsConnectionWatches(t *testing.T) {
	m := testRuntimeManager(t)
	m.registry = runtimegateway.NewRegistry()
	n, err := m.node("node")
	if err != nil {
		t.Fatal(err)
	}
	r := n.lifecycle
	r.awaiting = map[string]deployment.AllocationKey{"pending": {}}
	r.registry.WatchDevice("pending", r.connectionWake)
	m.stop()
	m.drain()
	if len(r.awaiting) != 0 || len(r.registry.PendingWaiters("pending")) != 0 {
		t.Fatal("stopped manager retained watch")
	}
}

func TestAwaitingConnectionPassSharesCancellationFence(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	entered := make(chan struct{})
	r := &runtimeLifecycle{ctx: ctx, stop: cancel, gate: make(chan struct{}, 1), lease: heldLease{}, awaiting: map[string]deployment.AllocationKey{"waiting": {}}, reader: &strictDeploymentReader{t: t, environmentAllocation: func(ctx context.Context, _ deployment.AllocationKey) (deployment.Allocation, error) {
		close(entered)
		<-ctx.Done()
		return deployment.Allocation{}, ctx.Err()
	}}}
	done := make(chan error, 1)
	go func() { done <- r.observeAwaitingConnections(t.Context()) }()
	<-entered
	r.cancelOperations()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("connection pass escaped cancellation")
	}
}
