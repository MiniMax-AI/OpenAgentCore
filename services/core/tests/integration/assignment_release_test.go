package integration

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/sessionpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

// TestDeletionReleaseReachesReconnectedRuntime checks that a release recorded
// while the Runtime was offline reaches it after it reconnects, and that only
// its acknowledgement records the release as applied.
func TestDeletionReleaseReachesReconnectedRuntime(t *testing.T) {
	h := newDispatchHarness(t)
	h.conn.Close()
	awaitDaemonRemoteCondition(t, t.Context(), 3*time.Second, "Runtime disconnected", func() bool {
		_, err := h.registry.LookupDevice(h.device.ID)
		return err != nil
	})
	if err := sessionService(t, h.s).DeleteSession(t.Context(), sessions.DeleteSessionCommand{TenantID: h.tenant, SessionID: h.session.ID}); err != nil {
		t.Fatal(err)
	}
	worker := startWorker(t, t.Context(), h.s, h.d)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })

	u, _ := url.Parse(strings.Replace(h.url, "http", "ws", 1) + "/api/v1/agent-daemon/ws")
	u.RawQuery = url.Values{"device_id": {h.device.ID}, "version": {proto.Version}}.Encode()
	conn, _, err := websocket.DefaultDialer.Dial(u.String(), http.Header{"Authorization": {"Bearer " + h.credential}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	h.conn = conn
	h.write("", proto.TypeHeartbeat, proto.HeartbeatPayload{HomeRemoval: proto.CapabilitySupported, SupportedAgentKinds: []proto.SupportedAgentKind{}})

	release := h.read(proto.TypeAssignmentRelease)
	var request proto.AssignmentReleasePayload
	if release.DecodePayload(&request) != nil || release.Assignment.SessionID != h.session.ID || release.Assignment.Epoch != 2 || !request.RemoveHome {
		t.Fatal("deletion did not release the assignment with home removal", release.Assignment, request)
	}
	applied := func() int64 {
		var epoch int64
		if err := h.s.pool.QueryRow(t.Context(), "SELECT applied_epoch FROM session_runtime_assignments WHERE session_id=$1", h.session.ID).Scan(&epoch); err != nil {
			t.Fatal(err)
		}
		return epoch
	}
	if applied() != 0 {
		t.Fatal("release recorded before the Runtime acknowledged it")
	}
	reply, err := release.Reply(proto.TypeAssignmentStatus, proto.AssignmentStatusPayload{State: proto.AssignmentHomeRemoved})
	if err != nil || conn.WriteJSON(reply) != nil {
		t.Fatal("cannot acknowledge the release", err)
	}
	awaitDaemonRemoteCondition(t, t.Context(), 3*time.Second, "release acknowledged", func() bool { return applied() == 2 })
}

// TestRevocationSettlesUndeliverableReleases checks that a release whose
// Runtime was revoked is settled with the revocation instead of staying
// pending: no Runtime is left to act on it.
func TestRevocationSettlesUndeliverableReleases(t *testing.T) {
	h := newDispatchHarness(t)
	ctx := t.Context()
	second, err := h.s.CreateSession(ctx, h.tenant, WithFixtureModelProvider(sessions.CreateSession{Creator: FixtureCreator(), Engine: "codex", IdempotencyKey: "second", Configuration: []byte(`{"agent":{"model":"test-model"},"environment":{"type":"none"}}`)}))
	if err != nil {
		t.Fatal(err)
	}
	if err := bindSessionDevice(t, h.s, h.tenant, second.ID, h.device.ID); err != nil {
		t.Fatal(err)
	}
	service := sessionService(t, h.s)
	settled := func(session string) bool {
		var epoch, applied int64
		if err := h.s.pool.QueryRow(ctx, "SELECT epoch, applied_epoch FROM session_runtime_assignments WHERE session_id=$1", session).Scan(&epoch, &applied); err != nil {
			t.Fatal(err)
		}
		return epoch == 2 && applied == 2
	}
	if err := service.DeleteSession(ctx, sessions.DeleteSessionCommand{TenantID: h.tenant, SessionID: h.session.ID}); err != nil {
		t.Fatal(err)
	}
	if settled(h.session.ID) {
		t.Fatal("a release to an authorized Runtime was settled before delivery")
	}
	if err := service.RevokeDevice(ctx, h.tenant, h.device.ID); err != nil {
		t.Fatal(err)
	}
	if !settled(h.session.ID) {
		t.Fatal("revocation left the Runtime's release pending")
	}
	if err := service.DeleteSession(ctx, sessions.DeleteSessionCommand{TenantID: h.tenant, SessionID: second.ID}); err != nil {
		t.Fatal(err)
	}
	if !settled(second.ID) {
		t.Fatal("a release to a revoked Runtime was left pending")
	}
}

// TestReleaseRacingRevocationIsSettled checks that a release racing the
// revocation of its shared Runtime is settled whichever reaches the device row
// first: the revocation settles the releases it sees, and a release sees the
// revocation.
func TestReleaseRacingRevocationIsSettled(t *testing.T) {
	for _, first := range []string{"revocation", "release"} {
		t.Run(first, func(t *testing.T) {
			h := newDispatchHarness(t)
			ctx := t.Context()
			h.conn.Close()
			awaitDaemonRemoteCondition(t, ctx, 3*time.Second, "Runtime disconnected", func() bool {
				_, err := h.registry.LookupDevice(h.device.ID)
				return err != nil
			})
			// waiting reports whether the named query waits for a lock.
			waiting := func(query string) bool {
				var n int
				if err := h.s.pool.QueryRow(ctx, "SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock' AND query LIKE '-- name: ' || $1 || ' %'", query).Scan(&n); err != nil {
					t.Fatal(err)
				}
				return n == 1
			}
			service := sessionService(t, h.s)
			revoked := make(chan error, 1)
			tx, err := h.s.pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(context.Background())
			if first == "revocation" {
				// The open transaction holds the device row, so the revocation
				// starts first and the deletion queues behind it.
				if _, err := tx.Exec(ctx, "SELECT 1 FROM devices WHERE id = $1 FOR NO KEY UPDATE", h.device.ID); err != nil {
					t.Fatal(err)
				}
				go func() { revoked <- service.RevokeDevice(ctx, h.tenant, h.device.ID) }()
				awaitDaemonRemoteCondition(t, ctx, 3*time.Second, "revocation waits for the device", func() bool { return waiting("RevokeDevice") })
				deleted := make(chan error, 1)
				go func() {
					deleted <- service.DeleteSession(ctx, sessions.DeleteSessionCommand{TenantID: h.tenant, SessionID: h.session.ID})
				}()
				awaitDaemonRemoteCondition(t, ctx, 3*time.Second, "deletion ends or waits for the device", func() bool {
					return len(deleted) == 1 || waiting("LockAssignmentRuntime")
				})
				if err := tx.Rollback(ctx); err != nil {
					t.Fatal(err)
				}
				if err := <-deleted; err != nil {
					t.Fatal(err)
				}
			} else {
				// The open transaction records the release, and the revocation
				// runs before it commits.
				tenant, _ := pgunit.ParseID(h.tenant)
				session, _ := pgunit.ParseID(h.session.ID)
				if err := sessionpg.BindSession(sqlc.New(tx), tenant, session).ReleaseAssignment(ctx, true); err != nil {
					t.Fatal(err)
				}
				go func() { revoked <- service.RevokeDevice(ctx, h.tenant, h.device.ID) }()
				awaitDaemonRemoteCondition(t, ctx, 3*time.Second, "revocation ends or waits for the device", func() bool {
					return len(revoked) == 1 || waiting("RevokeDevice")
				})
				if err := tx.Commit(ctx); err != nil {
					t.Fatal(err)
				}
			}
			if err := <-revoked; err != nil {
				t.Fatal(err)
			}
			var epoch, applied int64
			if err := h.s.pool.QueryRow(ctx, "SELECT epoch, applied_epoch FROM session_runtime_assignments WHERE session_id=$1", h.session.ID).Scan(&epoch, &applied); err != nil {
				t.Fatal(err)
			}
			if epoch != 2 || applied != 2 {
				t.Fatalf("epoch %d, applied %d: the release racing the revocation stayed pending", epoch, applied)
			}
		})
	}
}
