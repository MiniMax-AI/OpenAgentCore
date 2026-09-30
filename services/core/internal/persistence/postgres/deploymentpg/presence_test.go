package deploymentpg_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/deploymentpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/providers"
)

func presenceContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// presenceFixture initializes a Docker deployment and enrolls two nodes, neither
// connected, and returns them with the current owner epoch.
func presenceFixture(t *testing.T) (fixture, string, string, uint64) {
	t.Helper()
	f := newFixture(t)
	changes, _ := f.execution(t)
	_, view := f.initialize(t, changes, sandbox.Selection{Provider: "docker", DeploymentSpec: testSpecification("docker")})
	node := f.enroll(t, view, deployment.Capacity{MaxActive: 1, MaxRetained: 1})
	other := f.enroll(t, view, deployment.Capacity{MaxActive: 1, MaxRetained: 1})
	epoch, err := f.adapter.OwnerEpoch(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return f, node.NodeID, other.NodeID, epoch
}

// presenceWaitBlocked returns once blocker holds up another backend.
func presenceWaitBlocked(t *testing.T, ctx context.Context, pool *pgxpool.Pool, blocker int32, done <-chan error) {
	t.Helper()
	for {
		var blocked bool
		if err := pool.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)))", blocker).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			return
		}
		select {
		case err := <-done:
			t.Fatal("operation bypassed the node lock", err)
		case <-ctx.Done():
			t.Fatal("node lock wait not observed")
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func assertPresence(t *testing.T, pool *pgxpool.Pool, node, want string) {
	t.Helper()
	var actual pgtype.UUID
	// Wait for any canceled transaction to roll back before reading its outcome.
	if err := pool.QueryRow(presenceContext(t), "SELECT connection_id FROM runtime_nodes WHERE id=$1 FOR UPDATE", node).Scan(&actual); err != nil {
		t.Fatal(err)
	}
	got := ""
	if actual.Valid {
		got = uuid.UUID(actual.Bytes).String()
	}
	if got != want {
		t.Fatalf("presence = %q, want %q", got, want)
	}
}

func TestNodePresenceCanceledBlockedConnect(t *testing.T) {
	f, node, other, epoch := presenceFixture(t)
	ctx := presenceContext(t)
	lock, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Rollback(ctx)
	var blocker int32
	if err := lock.QueryRow(ctx, "SELECT pg_backend_pid() FROM runtime_nodes WHERE id=$1 FOR UPDATE", node).Scan(&blocker); err != nil {
		t.Fatal(err)
	}
	connecting, cancel := context.WithCancel(ctx)
	defer cancel()
	connection := uuid.NewString()
	done := make(chan error, 1)
	go func() { done <- f.service.ConnectNode(connecting, node, connection, epoch) }()
	presenceWaitBlocked(t, ctx, f.pool, blocker, done)
	var writer int32
	if err := f.pool.QueryRow(ctx, "SELECT pid FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)) LIMIT 1", blocker).Scan(&writer); err != nil {
		t.Fatal(err)
	}
	// Another node does not share the blocked presence transaction's lock.
	otherConnection := uuid.NewString()
	if err := f.service.ConnectNode(ctx, other, otherConnection, epoch); err != nil {
		t.Fatal(err)
	}
	if err := f.service.DisconnectNode(ctx, other, otherConnection, epoch); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal("blocked connect cancellation", err)
		}
	case <-ctx.Done():
		t.Fatal("blocked connect did not cancel")
	}
	if err := lock.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	// pgx cancellation can return before PostgreSQL stops the original statement.
	// Observe backend exit so a late autocommit cannot escape the assertion.
	for {
		var gone bool
		if err := f.pool.QueryRow(ctx, "SELECT NOT EXISTS (SELECT 1 FROM pg_stat_activity WHERE pid=$1)", writer).Scan(&gone); err != nil {
			t.Fatal(err)
		}
		if gone {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("canceled backend did not finish")
		case <-time.After(time.Millisecond):
		}
	}
	assertPresence(t, f.pool, node, "")
}

type cancelPresenceAfterUpdate struct{ cancel context.CancelFunc }

func (trace cancelPresenceAfterUpdate) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	return ctx
}
func (trace cancelPresenceAfterUpdate) TraceQueryEnd(_ context.Context, _ *pgx.Conn, result pgx.TraceQueryEndData) {
	if result.Err == nil && result.CommandTag.Update() {
		trace.cancel()
	}
}

func TestNodePresenceCanceledBeforeCommit(t *testing.T) {
	f, node, _, epoch := presenceFixture(t)
	ctx, cancel := context.WithCancel(presenceContext(t))
	defer cancel()
	cfg := f.pool.Config()
	// Cancel after PostgreSQL acknowledges UPDATE, before the adapter can commit it.
	cfg.ConnConfig.Tracer = cancelPresenceAfterUpdate{cancel: cancel}
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	adapter := deploymentpg.New(pgunit.NewPool(pool), f.cipher)
	service, err := deployment.NewService(adapter, adapter, providers.Builtin(), fixturePublicURL)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.ConnectNode(ctx, node, uuid.NewString(), epoch); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled UPDATE published presence", err)
	}
	assertPresence(t, f.pool, node, "")
}

func TestNodePresenceDisconnectWaitsForCommit(t *testing.T) {
	for _, guard := range []string{"matching", "newer_connection", "newer_epoch"} {
		t.Run(guard, func(t *testing.T) {
			f, node, other, epoch := presenceFixture(t)
			ctx := presenceContext(t)
			pending, err := f.pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer pending.Rollback(ctx)
			current, cleanup := uuid.NewString(), ""
			cleanup = current
			cleanupEpoch := epoch
			want := ""
			if guard == "newer_connection" {
				cleanup = uuid.NewString()
				want = current
			}
			if guard == "newer_epoch" {
				cleanupEpoch--
				want = current
			}
			if _, err := pending.Exec(ctx, "UPDATE runtime_nodes SET connection_id=$2,connected_epoch=$3 WHERE id=$1", node, current, epoch); err != nil {
				t.Fatal(err)
			}
			var blocker int32
			if err := pending.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&blocker); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- f.service.DisconnectNode(ctx, node, cleanup, cleanupEpoch) }()
			presenceWaitBlocked(t, ctx, f.pool, blocker, done)
			otherConnection := uuid.NewString()
			if err := f.service.ConnectNode(ctx, other, otherConnection, epoch); err != nil {
				t.Fatal(err)
			}
			if err := f.service.DisconnectNode(ctx, other, otherConnection, epoch); err != nil {
				t.Fatal(err)
			}
			if err := pending.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-ctx.Done():
				t.Fatal("cleanup did not settle after commit")
			}
			assertPresence(t, f.pool, node, want)
		})
	}
}

// A current node connection survives callbacks from the previous owner epoch.
func TestNodeStaleEpochCannotReplaceCurrentConnection(t *testing.T) {
	f := newFixture(t)
	changes, _ := f.execution(t)
	installation, view := f.initialize(t, changes, sandbox.Selection{Provider: "docker", DeploymentSpec: testSpecification("docker")})
	node := f.enroll(t, view, deployment.Capacity{MaxActive: 1, MaxRetained: 4})
	epoch, err := f.adapter.OwnerEpoch(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	// A new execution owner claims the installation and fences the old epoch.
	if err := changes.Claim(t.Context(), installation); err != nil {
		t.Fatal(err)
	}
	connection := f.connect(t, node.NodeID)
	if err := f.service.ConnectNode(t.Context(), node.NodeID, uuid.NewString(), epoch); !errors.Is(err, deployment.ErrNodeCredential) {
		t.Fatal("old Core replaced new connection", err)
	}
	if err := f.service.DisconnectNode(t.Context(), node.NodeID, connection, epoch); err != nil {
		t.Fatal(err)
	}
	if err := f.service.Heartbeat(t.Context(), node.NodeID, connection, epoch, deployment.NodeHealth{ProviderReady: false}); !errors.Is(err, deployment.ErrNodeCredential) {
		t.Fatal("old Core rewrote health", err)
	}
	nodes, err := f.service.ListNodes(t.Context())
	if err != nil || !nodes[0].Online || !nodes[0].ProviderReady {
		t.Fatal("stale callback changed current epoch", nodes, err)
	}
}

func TestNodeDiagnosticReachesListAndDetail(t *testing.T) {
	f := newFixture(t)
	changes, _ := f.execution(t)
	_, view := f.initialize(t, changes, sandbox.Selection{Provider: "docker", DeploymentSpec: testSpecification("docker")})
	node := f.enroll(t, view, deployment.Capacity{MaxActive: 1, MaxRetained: 1})
	connection := f.connect(t, node.NodeID)
	epoch, err := f.adapter.OwnerEpoch(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		reported, want string
		ready          bool
	}{
		{reported: "docker_unavailable", want: "docker_unavailable"},
		{reported: "capacity_insufficient", want: "capacity_insufficient"},
		// Older nodes send provider_unavailable or nothing; unknown text is never stored.
		{reported: "provider_unavailable", want: "provider_unavailable"},
		{reported: "", want: ""},
		{reported: "dial unix /var/run/docker.sock: permission denied", want: "provider_unavailable"},
		{reported: "kvm_unavailable", want: "", ready: true},
	} {
		if err := f.service.Heartbeat(t.Context(), node.NodeID, connection, epoch, deployment.NodeHealth{ProviderReady: tc.ready, Diagnostic: tc.reported}); err != nil {
			t.Fatal(tc.reported, err)
		}
		list, err := f.service.ListNodes(t.Context())
		if err != nil || len(list) != 1 || list[0].Diagnostic != tc.want {
			t.Fatal(tc.reported, list, err)
		}
		detail, err := f.service.NodeDetail(t.Context(), node.NodeID, "1h")
		if err != nil || detail.Diagnostic != tc.want {
			t.Fatal(tc.reported, detail.Diagnostic, err)
		}
		var stored string
		if err := f.pool.QueryRow(t.Context(), "SELECT health::text FROM runtime_nodes WHERE id=$1", node.NodeID).Scan(&stored); err != nil || strings.Contains(stored, "/var/run") {
			t.Fatal("raw diagnostic stored", stored, err)
		}
	}
}

func TestNodeStatusUsesAuthenticatedFreshPresence(t *testing.T) {
	f := newFixture(t)
	changes, _ := f.execution(t)
	_, view := f.initialize(t, changes, sandbox.Selection{Provider: "docker", DeploymentSpec: testSpecification("docker")})
	node := f.enroll(t, view, deployment.Capacity{MaxActive: 1, MaxRetained: 4})
	status := func(connected, ready bool) {
		t.Helper()
		got, err := f.service.NodeStatus(t.Context(), node.NodeID, node.Credential)
		if err != nil || got.NodeID != node.NodeID || got.Connected != connected || got.ProviderReady != ready {
			t.Fatal(got, err)
		}
		raw, err := json.Marshal(got)
		if err != nil || strings.Contains(string(raw), "credential") || strings.Contains(string(raw), "backend_fingerprint") {
			t.Fatal("private identity leaked", string(raw), err)
		}
	}
	status(false, false)
	connection := f.connect(t, node.NodeID)
	status(true, true)
	if _, err := f.service.NodeStatus(t.Context(), node.NodeID, "different-credential"); !errors.Is(err, deployment.ErrNodeCredential) {
		t.Fatal("status admitted another credential", err)
	}
	epoch, err := f.adapter.OwnerEpoch(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := f.service.Heartbeat(t.Context(), node.NodeID, connection, epoch, deployment.NodeHealth{ProviderReady: false}); err != nil {
		t.Fatal(err)
	}
	status(true, false)
	if err := f.service.Heartbeat(t.Context(), node.NodeID, connection, epoch, deployment.NodeHealth{ProviderReady: true}); err != nil {
		t.Fatal(err)
	}
	status(true, true)
	if _, err := f.pool.Exec(t.Context(), "UPDATE runtime_nodes SET last_seen_at=clock_timestamp()-interval '46 seconds' WHERE id=$1", node.NodeID); err != nil {
		t.Fatal(err)
	}
	status(false, false)
	f.connect(t, node.NodeID)
	if _, err := f.pool.Exec(t.Context(), "UPDATE runtime_deployment SET owner_epoch=owner_epoch+1 WHERE singleton=true"); err != nil {
		t.Fatal(err)
	}
	status(false, false)
}
