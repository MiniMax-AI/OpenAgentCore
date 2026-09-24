package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

func runtimePresenceContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func runtimePresenceOtherNode(t *testing.T, s *Store) string {
	t.Helper()
	token, _, err := s.CreateRuntimeEnrollment(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	input := RuntimeNodeEnrollment{DeploymentGeneration: 1, SpecificationDigest: SandboxDeploymentTestSpec("docker").Digest("docker"), NodeID: uuid.NewString(), Credential: strings.Repeat("x", 64), Name: "presence-other", Provider: "docker", BackendFingerprint: strings.Repeat("b", 64), MaxActive: 1, MaxRetained: 1}
	if _, err := s.EnrollRuntimeNode(t.Context(), token, input); err != nil {
		t.Fatal(err)
	}
	return input.NodeID
}

func assertRuntimePresence(t *testing.T, s *Store, node, want string) {
	t.Helper()
	var actual pgtype.UUID
	// Wait for any canceled transaction to roll back before reading its outcome.
	if err := s.pool.QueryRow(runtimePresenceContext(t), "SELECT connection_id FROM runtime_nodes WHERE id=$1 FOR UPDATE", node).Scan(&actual); err != nil {
		t.Fatal(err)
	}
	if runtimeUUID(actual) != want {
		t.Fatalf("presence = %q, want %q", runtimeUUID(actual), want)
	}
}

func TestRuntimeNodePresenceCanceledBlockedConnect(t *testing.T) {
	s, _, d := managerFixture(t, 1, 1)
	other := runtimePresenceOtherNode(t, s)
	epoch := managerEpoch(t, s)
	ctx := runtimePresenceContext(t)
	runtimeSuspensionSQL(t, s.pool, "UPDATE runtime_nodes SET connection_id=NULL WHERE id=$1", d.LocalNodeID)
	lock, err := s.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Rollback(ctx)
	var blocker int32
	if err := lock.QueryRow(ctx, "SELECT pg_backend_pid() FROM runtime_nodes WHERE id=$1 FOR UPDATE", d.LocalNodeID).Scan(&blocker); err != nil {
		t.Fatal(err)
	}
	connecting, cancel := context.WithCancel(ctx)
	defer cancel()
	connection := uuid.NewString()
	done := make(chan error, 1)
	go func() { done <- s.ConnectRuntimeNode(connecting, d.LocalNodeID, connection, epoch) }()
	runtimeSuspensionWaitBlocked(t, ctx, s.pool, blocker, done)
	var writer int32
	if err := s.pool.QueryRow(ctx, "SELECT pid FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)) LIMIT 1", blocker).Scan(&writer); err != nil {
		t.Fatal(err)
	}
	// Another node does not share the blocked presence transaction's lock.
	otherConnection := uuid.NewString()
	if err := s.ConnectRuntimeNode(ctx, other, otherConnection, epoch); err != nil {
		t.Fatal(err)
	}
	if err := s.DisconnectRuntimeNode(ctx, other, otherConnection, epoch); err != nil {
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
		if err := s.pool.QueryRow(ctx, "SELECT NOT EXISTS (SELECT 1 FROM pg_stat_activity WHERE pid=$1)", writer).Scan(&gone); err != nil {
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
	assertRuntimePresence(t, s, d.LocalNodeID, "")
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

func TestRuntimeNodePresenceCanceledBeforeCommit(t *testing.T) {
	s, _, d := managerFixture(t, 1, 1)
	epoch := managerEpoch(t, s)
	runtimeSuspensionSQL(t, s.pool, "UPDATE runtime_nodes SET connection_id=NULL WHERE id=$1", d.LocalNodeID)
	ctx, cancel := context.WithCancel(runtimePresenceContext(t))
	defer cancel()
	cfg := s.pool.Config()
	// Cancel after PostgreSQL acknowledges UPDATE, before Store can commit it.
	cfg.ConnConfig.Tracer = cancelPresenceAfterUpdate{cancel: cancel}
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := New(pool).ConnectRuntimeNode(ctx, d.LocalNodeID, uuid.NewString(), epoch); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled UPDATE published presence", err)
	}
	assertRuntimePresence(t, s, d.LocalNodeID, "")
}

func TestRuntimeNodePresenceDisconnectWaitsForCommit(t *testing.T) {
	for _, guard := range []string{"matching", "newer_connection", "newer_epoch"} {
		t.Run(guard, func(t *testing.T) {
			s, _, d := managerFixture(t, 1, 1)
			other := runtimePresenceOtherNode(t, s)
			epoch := managerEpoch(t, s)
			ctx := runtimePresenceContext(t)
			runtimeSuspensionSQL(t, s.pool, "UPDATE runtime_nodes SET connection_id=NULL WHERE id=$1", d.LocalNodeID)
			pending, err := s.pool.Begin(ctx)
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
			if _, err := pending.Exec(ctx, "UPDATE runtime_nodes SET connection_id=$2,connected_epoch=$3 WHERE id=$1", d.LocalNodeID, current, epoch); err != nil {
				t.Fatal(err)
			}
			var blocker int32
			if err := pending.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&blocker); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- s.DisconnectRuntimeNode(ctx, d.LocalNodeID, cleanup, cleanupEpoch) }()
			runtimeSuspensionWaitBlocked(t, ctx, s.pool, blocker, done)
			otherConnection := uuid.NewString()
			if err := s.ConnectRuntimeNode(ctx, other, otherConnection, epoch); err != nil {
				t.Fatal(err)
			}
			if err := s.DisconnectRuntimeNode(ctx, other, otherConnection, epoch); err != nil {
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
			assertRuntimePresence(t, s, d.LocalNodeID, want)
		})
	}
}
