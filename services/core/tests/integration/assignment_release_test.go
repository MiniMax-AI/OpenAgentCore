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
