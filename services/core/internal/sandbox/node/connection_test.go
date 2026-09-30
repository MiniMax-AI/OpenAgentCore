package node

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/docker"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/gorilla/websocket"
)

func nodeEndpoint(origin, id string) string {
	return "ws" + strings.TrimPrefix(origin, "http") + "?node_id=" + id
}
func assertDuplicateRejected(t *testing.T, origin, id, credential string) {
	t.Helper()
	conn, response, err := websocket.DefaultDialer.Dial(nodeEndpoint(origin, id), http.Header{"Authorization": []string{"Bearer " + credential}})
	if conn != nil {
		conn.Close()
	}
	if response != nil {
		defer response.Body.Close()
	}
	if err == nil || response == nil || response.StatusCode != http.StatusConflict {
		t.Fatalf("duplicate connection was not rejected with 409: response=%v err=%v", responseStatus(response), err)
	}
}
func responseStatus(r *http.Response) int {
	if r == nil {
		return 0
	}
	return r.StatusCode
}
func reservationReleased(h *Hub, id string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	_, held := h.reservations[id]
	return !held
}
func connectRawNode(t *testing.T, origin string, id Identity) *websocket.Conn {
	t.Helper()
	conn, _, err := websocket.DefaultDialer.Dial(nodeEndpoint(origin, id.NodeID), http.Header{"Authorization": []string{"Bearer test"}})
	if err != nil {
		t.Fatal(err)
	}
	if err = writeFrame(conn, frame{Type: "hello", Identity: &id, Health: &Health{ProviderReady: true, ObservedAt: time.Now().UTC()}}); err != nil {
		conn.Close()
		t.Fatal(err)
	}
	if _, err = readFrame(conn); err != nil {
		conn.Close()
		t.Fatal(err)
	}
	return conn
}

func TestHubReservesIdentityAcrossConcurrentOpeningHandshakes(t *testing.T) {
	id := identity()
	var connected atomic.Int32
	hub := NewHub(HubOptions{Authenticate: func(context.Context, string, string) (Identity, error) { return id, nil }, OwnerEpoch: func(context.Context) (uint64, error) { return 1, nil }, Connected: func(context.Context, Identity, string, uint64) error { connected.Add(1); return nil }})
	server := httptest.NewServer(hub)
	defer server.Close()
	defer hub.Close()
	type result struct {
		conn   *websocket.Conn
		status int
		err    error
	}
	results := make(chan result, 2)
	start := make(chan struct{})
	for range 2 {
		go func() {
			<-start
			conn, response, err := websocket.DefaultDialer.Dial(nodeEndpoint(server.URL, id.NodeID), http.Header{"Authorization": []string{"Bearer test"}})
			status := responseStatus(response)
			if response != nil {
				response.Body.Close()
			}
			results <- result{conn, status, err}
		}()
	}
	close(start)
	var winner *websocket.Conn
	accepted, rejected := 0, 0
	for range 2 {
		r := <-results
		if r.err == nil {
			accepted++
			winner = r.conn
		} else if r.status == http.StatusConflict {
			rejected++
		} else {
			t.Errorf("unexpected handshake result: status=%d err=%v", r.status, r.err)
		}
	}
	if winner != nil {
		defer winner.Close()
	}
	if accepted != 1 || rejected != 1 || connected.Load() != 0 {
		t.Fatalf("opening identity was not exclusive: accepted=%d rejected=%d callbacks=%d", accepted, rejected, connected.Load())
	}
	// No hello was sent: even an unfinished handshake owns the reservation, and
	// its failure must release it without touching persisted node presence.
	assertDuplicateRejected(t, server.URL, id.NodeID, "test")
	winner.Close()
	wait(t, func() bool { return reservationReleased(hub, id.NodeID) })
	conn := connectRawNode(t, server.URL, id)
	defer conn.Close()
	wait(t, func() bool { return hub.Online(id.NodeID) })
	if connected.Load() != 1 {
		t.Fatal("failed handshake changed node presence")
	}
}

func TestCopiedIdentityCannotReplaceNodeWithInflightCreate(t *testing.T) {
	id := identity()
	var credential string
	var authenticated, connected atomic.Int32
	duplicateAttempt := make(chan struct{}, 1)
	hub := NewHub(HubOptions{Authenticate: func(ctx context.Context, node, token string) (Identity, error) {
		if node != id.NodeID || token != credential {
			return Identity{}, ErrAuthentication
		}
		if authenticated.Add(1) > 1 {
			select {
			case duplicateAttempt <- struct{}{}:
			default:
			}
		}
		return id, nil
	}, OwnerEpoch: func(context.Context) (uint64, error) { return 1, nil }, Connected: func(context.Context, Identity, string, uint64) error { connected.Add(1); return nil }})
	server := httptest.NewServer(hub)
	defer server.Close()
	defer hub.Close()
	dir := stateDir(t)
	stored, err := InitIdentity(dir, server.URL, id)
	if err != nil {
		t.Fatal(err)
	}
	credential = stored.Credential
	duplicateDir := stateDir(t)
	if err = writeIdentity(duplicateDir, stored); err != nil {
		t.Fatal(err)
	}
	original := &fakeProvider{started: make(chan struct{}), release: make(chan struct{})}
	duplicate := &fakeProvider{}
	var release sync.Once
	defer release.Do(func() { close(original.release) })
	start := func(dir string, p sandbox.SandboxProvider) (context.CancelFunc, chan error) {
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() {
			done <- Run(ctx, AgentConfig{CoreURL: server.URL, StateDirectory: dir, Identity: id, Credential: credential, Provider: p, Probe: probe})
		}()
		return cancel, done
	}
	stopOriginal, originalDone := start(dir, original)
	defer func() {
		release.Do(func() { close(original.release) })
		stopOriginal()
		if err := <-originalDone; err != nil {
			t.Error(err)
		}
	}()
	wait(t, func() bool { return hub.Online(id.NodeID) })
	hub.mu.Lock()
	first := hub.peers[id.NodeID]
	hub.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	proxy := hub.Proxy(id.NodeID, "docker", docker.Operations(), 1)
	r := reference()
	created := make(chan error, 1)
	go func() { _, err := proxy.Create(ctx, sandbox.Bootstrap{Reference: r}); created <- err }()
	<-original.started
	stopDuplicate, duplicateDone := start(duplicateDir, duplicate)
	defer func() {
		stopDuplicate()
		if err := <-duplicateDone; err != nil {
			t.Error(err)
		}
	}()
	select {
	case <-duplicateAttempt:
	case <-time.After(time.Second):
		t.Fatal("duplicate did not attempt connection")
	}
	for range 3 {
		assertDuplicateRejected(t, server.URL, id.NodeID, credential)
	}
	hub.mu.Lock()
	retained := hub.peers[id.NodeID] == first
	hub.mu.Unlock()
	if !retained || connected.Load() != 1 {
		t.Fatal("duplicate replaced original connection or persisted its presence")
	}
	release.Do(func() { close(original.release) })
	if err = <-created; err != nil {
		t.Fatal(err)
	}
	if _, err = proxy.GetInfo(ctx, r); err != nil {
		t.Fatal(err)
	}
	original.mu.Lock()
	creates, reads := original.creates, original.reads
	original.mu.Unlock()
	duplicate.mu.Lock()
	duplicateEffects := duplicate.creates + duplicate.reads + duplicate.kills
	duplicate.mu.Unlock()
	if creates != 1 || reads != 1 || duplicateEffects != 0 {
		t.Fatalf("work moved/replayed: creates=%d reads=%d duplicate=%d", creates, reads, duplicateEffects)
	}
}

func TestReservationRemainsUntilFencedDisconnectFinishes(t *testing.T) {
	id := identity()
	retiring := make(chan struct{})
	finish := make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(finish) })
	var mu sync.Mutex
	active := ""
	connections := 0
	var callbackErr error
	hub := NewHub(HubOptions{Authenticate: func(context.Context, string, string) (Identity, error) { return id, nil }, OwnerEpoch: func(context.Context) (uint64, error) { return 9, nil }, Connected: func(ctx context.Context, id Identity, connection string, epoch uint64) error {
		mu.Lock()
		defer mu.Unlock()
		if epoch != 9 {
			callbackErr = errors.New("incorrect connect epoch")
		}
		active = connection
		connections++
		return nil
	}, Disconnected: func(ctx context.Context, id Identity, connection string, epoch uint64) {
		mu.Lock()
		first := connections == 1
		mu.Unlock()
		if first {
			close(retiring)
			<-finish
		}
		mu.Lock()
		defer mu.Unlock()
		if epoch != 9 {
			callbackErr = errors.New("incorrect disconnect epoch")
		}
		if active == connection {
			active = ""
		}
	}})
	server := httptest.NewServer(hub)
	defer server.Close()
	defer hub.Close()
	first := connectRawNode(t, server.URL, id)
	wait(t, func() bool { return hub.Online(id.NodeID) })
	first.Close()
	select {
	case <-retiring:
	case <-time.After(time.Second):
		t.Fatal("disconnect callback did not start")
	}
	assertDuplicateRejected(t, server.URL, id.NodeID, "test")
	mu.Lock()
	count := connections
	mu.Unlock()
	if count != 1 {
		t.Fatal("new connection admitted before disconnect settled")
	}
	once.Do(func() { close(finish) })
	wait(t, func() bool { return reservationReleased(hub, id.NodeID) })
	next := connectRawNode(t, server.URL, id)
	defer next.Close()
	wait(t, func() bool { return hub.Online(id.NodeID) })
	hub.mu.Lock()
	current := hub.peers[id.NodeID].id
	hub.mu.Unlock()
	mu.Lock()
	defer mu.Unlock()
	if active != current || connections != 2 || callbackErr != nil {
		t.Fatalf("stale disconnect cleared current presence: count=%d error=%v", connections, callbackErr)
	}
}
