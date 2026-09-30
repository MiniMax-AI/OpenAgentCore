package node

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/docker"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

func TestCoreRestartFencesOldConnectionAndNodeRestartKeepsIdentity(t *testing.T) {
	id := identity()
	var credential string
	options := func(epoch uint64) HubOptions {
		return HubOptions{Authenticate: func(context.Context, string, string) (Identity, error) { return id, nil }, OwnerEpoch: func(context.Context) (uint64, error) { return epoch, nil }}
	}
	first := NewHub(options(4))
	second := NewHub(options(5))
	defer first.Close()
	defer second.Close()
	var mu sync.Mutex
	current := first
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { mu.Lock(); h := current; mu.Unlock(); h.ServeHTTP(w, r) }))
	defer server.Close()
	dir := stateDir(t)
	stored, err := InitIdentity(dir, server.URL, id)
	if err != nil {
		t.Fatal(err)
	}
	credential = stored.Credential
	start := func() (context.CancelFunc, chan error) {
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() {
			done <- Run(ctx, AgentConfig{CoreURL: server.URL, StateDirectory: dir, Identity: id, Credential: credential, Provider: &fakeProvider{}, Probe: probe})
		}()
		return cancel, done
	}
	stop, done := start()
	defer func() { stop() }()
	wait(t, func() bool { return first.Online(id.NodeID) })
	mu.Lock()
	current = second
	mu.Unlock()
	first.Close()
	wait(t, func() bool { return second.Online(id.NodeID) })
	if first.Online(id.NodeID) {
		t.Fatal("old owner remained online")
	}
	if _, err = first.Proxy(id.NodeID, "docker", docker.Operations(), 1).GetInfo(context.Background(), reference()); !errors.Is(err, sandbox.ErrComputeUnconfirmed) {
		t.Fatalf("old owner request = %v", err)
	}
	if _, err = second.Proxy(id.NodeID, "docker", docker.Operations(), 1).GetInfo(context.Background(), reference()); err != nil {
		t.Fatal(err)
	}
	stop()
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	wait(t, func() bool { return !second.Online(id.NodeID) })
	persisted, err := LoadIdentity(dir)
	if err != nil || persisted.OwnerEpoch != 5 || persisted.Credential != credential {
		t.Fatal("restart identity changed")
	}
	stop, done = start()
	wait(t, func() bool { return second.Online(id.NodeID) })
	stop()
	if err = <-done; err != nil {
		t.Fatal(err)
	}
}

func TestAgentRejectsOwnerEpochRollback(t *testing.T) {
	id := identity()
	upgrade := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, e := upgrade.Upgrade(w, r, nil)
		if e != nil {
			return
		}
		defer conn.Close()
		_, _ = readFrame(conn)
		_ = writeFrame(conn, frame{Type: "welcome", ConnectionID: uuid.NewString(), OwnerEpoch: 3})
		_, _ = readFrame(conn)
	}))
	defer server.Close()
	dir := stateDir(t)
	stored, err := InitIdentity(dir, server.URL, id)
	if err != nil {
		t.Fatal(err)
	}
	stored.OwnerEpoch = 4
	if err = writeIdentity(dir, stored); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err = Run(ctx, AgentConfig{CoreURL: server.URL, StateDirectory: dir, Identity: id, Credential: stored.Credential, Provider: &fakeProvider{}, Probe: probe})
	if !errors.Is(err, sandbox.ErrOwnership) {
		t.Fatalf("stale owner = %v", err)
	}
}

func TestHeartbeatAcknowledgementKeepsIdleConnectionAlive(t *testing.T) {
	id := identity()
	beats := make(chan Health, 1)
	var heartbeats int
	hub := NewHub(HubOptions{Authenticate: func(context.Context, string, string) (Identity, error) { return id, nil }, OwnerEpoch: func(context.Context) (uint64, error) { return 1, nil }, Heartbeat: func(ctx context.Context, id Identity, connection string, epoch uint64, h Health) error {
		heartbeats++
		if heartbeats == 1 {
			return nil
		}
		select {
		case beats <- h:
		default:
		}
		return nil
	}})
	server := httptest.NewServer(hub)
	defer server.Close()
	defer hub.Close()
	dir := stateDir(t)
	stored, err := InitIdentity(dir, server.URL, id)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, AgentConfig{CoreURL: server.URL, StateDirectory: dir, Identity: id, Credential: stored.Credential, Provider: &fakeProvider{}, Probe: probe})
	}()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	select {
	case h := <-beats:
		if !h.ProviderReady || h.ObservedAt.IsZero() {
			t.Fatal("invalid heartbeat")
		}
	case <-time.After(12 * time.Second):
		t.Fatal("missing heartbeat")
	}
	if !hub.Online(id.NodeID) {
		t.Fatal("idle node disconnected")
	}
	if _, err = hub.Proxy(id.NodeID, "docker", docker.Operations(), 1).GetInfo(ctx, reference()); err != nil {
		t.Fatal(err)
	}
}

func TestHubExpiresSilentNode(t *testing.T) {
	if testing.Short() {
		t.Skip("real heartbeat timeout")
	}
	id := identity()
	hub := NewHub(HubOptions{Authenticate: func(context.Context, string, string) (Identity, error) { return id, nil }, OwnerEpoch: func(context.Context) (uint64, error) { return 1, nil }})
	server := httptest.NewServer(hub)
	defer server.Close()
	defer hub.Close()
	url := "ws" + strings.TrimPrefix(server.URL, "http") + "?node_id=" + id.NodeID
	conn, _, err := websocket.DefaultDialer.Dial(url, http.Header{"Authorization": []string{"Bearer test"}})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err = writeFrame(conn, frame{Type: "hello", Identity: &id, Health: &Health{ProviderReady: true, ObservedAt: time.Now().UTC()}}); err != nil {
		t.Fatal(err)
	}
	if _, err = readFrame(conn); err != nil {
		t.Fatal(err)
	}
	wait(t, func() bool { return hub.Online(id.NodeID) })
	assertDuplicateRejected(t, server.URL, id.NodeID, "test")
	_ = conn.SetReadDeadline(time.Now().Add(38 * time.Second))
	if _, err = readFrame(conn); err == nil {
		t.Fatal("silent node connection survived deadline")
	}
	wait(t, func() bool { return !hub.Online(id.NodeID) })
	wait(t, func() bool { return reservationReleased(hub, id.NodeID) })
	replacement := connectRawNode(t, server.URL, id)
	defer replacement.Close()
	wait(t, func() bool { return hub.Online(id.NodeID) })
}

func TestDegradedNodeRetainsObservationAndCleanup(t *testing.T) {
	id := identity()
	health := make(chan Health, 1)
	hub := NewHub(HubOptions{Authenticate: func(context.Context, string, string) (Identity, error) { return id, nil }, OwnerEpoch: func(context.Context) (uint64, error) { return 1, nil }, Heartbeat: func(ctx context.Context, id Identity, connection string, epoch uint64, h Health) error {
		select {
		case health <- h:
		default:
		}
		return nil
	}})
	server := httptest.NewServer(hub)
	defer server.Close()
	defer hub.Close()
	dir := stateDir(t)
	stored, err := InitIdentity(dir, server.URL, id)
	if err != nil {
		t.Fatal(err)
	}
	p := &fakeProvider{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, AgentConfig{CoreURL: server.URL, StateDirectory: dir, Identity: id, Credential: stored.Credential, Provider: p, Probe: func(context.Context) (Health, error) { return Health{}, errors.New("private backend detail") }})
	}()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	wait(t, func() bool { return hub.Online(id.NodeID) })
	select {
	case h := <-health:
		if h.ProviderReady || h.Diagnostic != "provider_unavailable" {
			t.Fatalf("unsafe health: %+v", h)
		}
	case <-time.After(time.Second):
		t.Fatal("missing health")
	}
	proxy := hub.Proxy(id.NodeID, "docker", docker.Operations(), 1)
	r := reference()
	if _, err = proxy.Create(ctx, sandbox.Bootstrap{Reference: r}); !errors.Is(err, sandbox.ErrComputeUnconfirmed) {
		t.Fatalf("create = %v", err)
	}
	if _, err = proxy.GetInfo(ctx, r); err != nil {
		t.Fatal(err)
	}
	if err = proxy.Kill(ctx, r); err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.creates != 0 || p.kills != 1 || p.reads != 1 {
		t.Fatal("degraded node incorrectly dispatched work")
	}
}

func TestCorruptOrMismatchedIdentityNeverRotates(t *testing.T) {
	id := identity()
	dir := stateDir(t)
	stored, err := InitIdentity(dir, "https://core.example.test", id)
	if err != nil {
		t.Fatal(err)
	}
	mismatch := id
	mismatch.BackendFingerprint = strings.Repeat("2", 64)
	if _, err = InitIdentity(dir, stored.CoreURL, mismatch); err == nil {
		t.Fatal("adopted wrong backend")
	}
	original, err := LoadIdentity(dir)
	if err != nil || original.Credential != stored.Credential {
		t.Fatal("credential rotated")
	}
	if err = os.WriteFile(filepath.Join(dir, "identity.json"), []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = InitIdentity(dir, stored.CoreURL, id); err == nil {
		t.Fatal("corrupt identity replaced")
	}
	if err = os.Remove(filepath.Join(dir, "identity.json")); err != nil {
		t.Fatal(err)
	}
	if _, err = LoadIdentity(dir); err == nil {
		t.Fatal("missing identity recreated by load")
	}
}
