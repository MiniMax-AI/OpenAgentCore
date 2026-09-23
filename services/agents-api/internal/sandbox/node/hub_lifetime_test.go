package node

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
	"github.com/gorilla/websocket"
)

func beginRawNode(t *testing.T, origin string, id Identity) *websocket.Conn {
	t.Helper()
	conn, _, err := websocket.DefaultDialer.Dial(nodeEndpoint(origin, id.NodeID), http.Header{"Authorization": []string{"Bearer test"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err = writeFrame(conn, frame{Type: "hello", Identity: &id, Health: &Health{ProviderReady: true}}); err != nil {
		t.Fatal(err)
	}
	return conn
}

func serveRawInfo(conn *websocket.Conn) {
	for {
		f, err := readFrame(conn)
		if err != nil || f.Request == nil {
			return
		}
		q := f.Request
		if writeFrame(conn, frame{Type: "response", Response: &response{ID: q.ID, ConnectionID: q.ConnectionID, Info: &sandbox.Info{Reference: q.Reference, ProviderID: "retained", State: "running"}}}) != nil {
			return
		}
	}
}

func assertInfoResponsive(t *testing.T, hub *Hub, id Identity) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	info, err := hub.Proxy(id.NodeID, id.Provider).GetInfo(ctx, reference())
	if err != nil || info.ProviderID != "retained" {
		t.Fatalf("unrelated node RPC blocked: info=%+v err=%v", info, err)
	}
}

func assertPrompt(t *testing.T, operation func()) {
	t.Helper()
	done := make(chan struct{})
	go func() { operation(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("operation waited for blocked callback")
	}
}

func TestHubCloseCancelsEveryOpeningCallback(t *testing.T) {
	for _, stage := range []string{"authenticate", "owner", "connected", "heartbeat"} {
		t.Run(stage, func(t *testing.T) {
			id := identity()
			entered, returned := make(chan struct{}), make(chan struct{})
			block := func(ctx context.Context) {
				if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > callbackTimeout {
					t.Error("callback lacks bounded deadline")
				}
				close(entered)
				<-ctx.Done()
				close(returned)
			}
			var cleanups atomic.Int32
			hub := NewHub(HubOptions{
				Authenticate: func(ctx context.Context, _, _ string) (Identity, error) {
					if stage == "authenticate" {
						block(ctx)
					}
					return id, nil
				},
				OwnerEpoch: func(ctx context.Context) (uint64, error) {
					if stage == "owner" {
						block(ctx)
					}
					return 7, nil
				},
				Connected: func(ctx context.Context, _ Identity, _ string, _ uint64) error {
					if stage == "connected" {
						block(ctx)
					}
					return nil
				},
				Heartbeat: func(ctx context.Context, _ Identity, _ string, _ uint64, _ Health) error {
					if stage == "heartbeat" {
						block(ctx)
					}
					return nil
				},
				Disconnected: func(ctx context.Context, _ Identity, _ string, epoch uint64) {
					if ctx.Err() != nil || epoch != 7 {
						t.Error("cleanup inherited canceled context or wrong epoch")
					}
					if _, ok := ctx.Deadline(); !ok {
						t.Error("cleanup lacks deadline")
					}
					cleanups.Add(1)
				},
			})
			server := httptest.NewServer(hub)
			defer server.Close()
			defer hub.Close()
			clientDone := make(chan struct{})
			go func() {
				defer close(clientDone)
				conn, response, err := websocket.DefaultDialer.Dial(nodeEndpoint(server.URL, id.NodeID), http.Header{"Authorization": []string{"Bearer test"}})
				if response != nil {
					response.Body.Close()
				}
				if err != nil {
					return
				}
				defer conn.Close()
				_ = writeFrame(conn, frame{Type: "hello", Identity: &id, Health: &Health{ProviderReady: true}})
				if f, err := readFrame(conn); err == nil {
					t.Errorf("canceled opening published welcome: %s", f.Type)
				}
			}()
			select {
			case <-entered:
			case <-time.After(time.Second):
				t.Fatal("callback not entered")
			}
			assertPrompt(t, hub.Close)
			select {
			case <-returned:
			case <-time.After(time.Second):
				t.Fatal("callback did not receive cancellation")
			}
			select {
			case <-clientDone:
			case <-time.After(time.Second):
				t.Fatal("opening socket was not closed")
			}
			wait(t, func() bool { return reservationReleased(hub, id.NodeID) })
			if hub.Online(id.NodeID) {
				t.Fatal("closed Hub published peer")
			}
			want := int32(0)
			if stage == "connected" || stage == "heartbeat" {
				want = 1
			}
			if cleanups.Load() != want {
				t.Fatalf("cleanups=%d want=%d", cleanups.Load(), want)
			}
		})
	}
}

func TestSlowOpeningAndClosingLeaveOtherNodesResponsive(t *testing.T) {
	fast, slow := identity(), identity()
	opening, closing, finish := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var release sync.Once
	defer release.Do(func() { close(finish) })
	hub := NewHub(HubOptions{
		Authenticate: func(_ context.Context, id, _ string) (Identity, error) {
			if id == slow.NodeID {
				return slow, nil
			}
			return fast, nil
		},
		OwnerEpoch: func(context.Context) (uint64, error) { return 1, nil },
		Connected: func(ctx context.Context, id Identity, _ string, _ uint64) error {
			if id.NodeID == slow.NodeID {
				close(opening)
				<-ctx.Done()
				return ctx.Err()
			}
			return nil
		},
		Disconnected: func(_ context.Context, id Identity, _ string, _ uint64) {
			if id.NodeID == slow.NodeID {
				close(closing)
				<-finish
			}
		},
	})
	server := httptest.NewServer(hub)
	defer server.Close()
	defer hub.Close()
	live := connectRawNode(t, server.URL, fast)
	defer live.Close()
	go serveRawInfo(live)
	beginRawNode(t, server.URL, slow)
	select {
	case <-opening:
	case <-time.After(time.Second):
		t.Fatal("opening callback not reached")
	}
	assertInfoResponsive(t, hub, fast)
	assertDuplicateRejected(t, server.URL, slow.NodeID, "test")
	assertPrompt(t, func() { hub.Disconnect(slow.NodeID) })
	select {
	case <-closing:
	case <-time.After(time.Second):
		t.Fatal("cleanup not reached")
	}
	assertDuplicateRejected(t, server.URL, slow.NodeID, "test")
	assertInfoResponsive(t, hub, fast)
	assertPrompt(t, hub.Close)
	if reservationReleased(hub, slow.NodeID) {
		t.Fatal("closing reservation released before callback returned")
	}
	release.Do(func() { close(finish) })
	wait(t, func() bool { return reservationReleased(hub, slow.NodeID) })
}

func TestHubCleansPresenceAfterPartialOpeningFailure(t *testing.T) {
	for _, stage := range []string{"connected", "heartbeat"} {
		t.Run(stage, func(t *testing.T) {
			id := identity()
			var mu sync.Mutex
			persisted := ""
			var connects, cleanups int
			hub := NewHub(HubOptions{
				Authenticate: func(context.Context, string, string) (Identity, error) { return id, nil },
				OwnerEpoch:   func(context.Context) (uint64, error) { return 11, nil },
				Connected: func(_ context.Context, _ Identity, connection string, _ uint64) error {
					mu.Lock()
					defer mu.Unlock()
					persisted = connection
					connects++
					if stage == "connected" {
						return errors.New("reply lost after commit")
					}
					return nil
				},
				Heartbeat: func(context.Context, Identity, string, uint64, Health) error { return errors.New("heartbeat failed") },
				Disconnected: func(_ context.Context, _ Identity, connection string, epoch uint64) {
					mu.Lock()
					defer mu.Unlock()
					if epoch == 11 && persisted == connection {
						persisted = ""
					}
					cleanups++
				},
			})
			server := httptest.NewServer(hub)
			defer server.Close()
			defer hub.Close()
			conn := beginRawNode(t, server.URL, id)
			if f, err := readFrame(conn); err == nil {
				t.Fatalf("failed opening welcomed: %s", f.Type)
			}
			wait(t, func() bool { return reservationReleased(hub, id.NodeID) })
			mu.Lock()
			defer mu.Unlock()
			if persisted != "" || connects != 1 || cleanups != 1 || hub.Online(id.NodeID) {
				t.Fatalf("partial presence leaked: persisted=%q connects=%d cleanups=%d", persisted, connects, cleanups)
			}
		})
	}
}

func TestHubSendQueueRespectsCallerCancellation(t *testing.T) {
	id := identity()
	hub := NewHub(HubOptions{Authenticate: func(context.Context, string, string) (Identity, error) { return id, nil }, OwnerEpoch: func(context.Context) (uint64, error) { return 1, nil }})
	server := httptest.NewServer(hub)
	defer server.Close()
	defer hub.Close()
	conn := connectRawNode(t, server.URL, id)
	defer conn.Close()
	wait(t, func() bool { return hub.Online(id.NodeID) })
	hub.mu.Lock()
	p := hub.peers[id.NodeID]
	hub.mu.Unlock()
	if err := p.lockSend(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := hub.Proxy(id.NodeID, id.Provider).GetInfo(ctx, reference()); done <- err }()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("queued request: %v", err)
		}
	case <-time.After(time.Second):
		p.unlockSend()
		t.Fatal("send queue ignored cancellation")
	}
	p.unlockSend()
	p.mu.Lock()
	pending := len(p.pending)
	p.mu.Unlock()
	if pending != 0 || !hub.Online(id.NodeID) {
		t.Fatalf("unsent cancellation leaked pending=%d or disconnected peer", pending)
	}
	go serveRawInfo(conn)
	assertInfoResponsive(t, hub, id)
}

func TestHubCallBoundsOwnershipBeforeDispatch(t *testing.T) {
	entered := make(chan struct{})
	hub := NewHub(HubOptions{OwnerEpoch: func(ctx context.Context) (uint64, error) {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > callbackTimeout {
			t.Error("ownership callback unbounded")
		}
		close(entered)
		<-ctx.Done()
		return 0, ctx.Err()
	}})
	defer hub.Close()
	done := make(chan error, 1)
	go func() {
		_, err := hub.call(context.Background(), identity().NodeID, request{Operation: "get_info"})
		done <- err
	}()
	<-entered
	assertPrompt(t, hub.Close)
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("ownership callback survived Hub.Close")
	}
}

func TestHubRetainsOpeningReservationUntilCanceledCallbackReturns(t *testing.T) {
	id := identity()
	entered, canceled, finish := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var release sync.Once
	defer release.Do(func() { close(finish) })
	var mu sync.Mutex
	persisted := ""
	cleanups := 0
	hub := NewHub(HubOptions{
		Authenticate: func(context.Context, string, string) (Identity, error) { return id, nil },
		OwnerEpoch:   func(context.Context) (uint64, error) { return 4, nil },
		Connected: func(ctx context.Context, _ Identity, connection string, _ uint64) error {
			close(entered)
			<-ctx.Done()
			close(canceled)
			// Simulate a database call settling after cancellation, with an
			// uncertain committed write. The handler must await its return.
			<-finish
			mu.Lock()
			persisted = connection
			mu.Unlock()
			return nil
		},
		Disconnected: func(ctx context.Context, _ Identity, connection string, epoch uint64) {
			mu.Lock()
			defer mu.Unlock()
			if ctx.Err() != nil {
				t.Error("cleanup already canceled")
			}
			if epoch == 4 && persisted == connection {
				persisted = ""
			}
			cleanups++
		},
	})
	server := httptest.NewServer(hub)
	defer server.Close()
	defer hub.Close()
	beginRawNode(t, server.URL, id)
	<-entered
	assertPrompt(t, hub.Close)
	<-canceled
	if reservationReleased(hub, id.NodeID) {
		t.Fatal("opening reservation released before callback settled")
	}
	mu.Lock()
	count := cleanups
	mu.Unlock()
	if count != 0 || hub.Online(id.NodeID) {
		t.Fatal("cleanup or publication overtook unfinished callback")
	}
	release.Do(func() { close(finish) })
	wait(t, func() bool { return reservationReleased(hub, id.NodeID) })
	mu.Lock()
	defer mu.Unlock()
	if persisted != "" || cleanups != 1 || hub.Online(id.NodeID) {
		t.Fatal("late opening write was not cleaned")
	}
}

func TestHubDisconnectCancelsPeriodicHeartbeatCallback(t *testing.T) {
	id := identity()
	entered := make(chan struct{})
	var heartbeats atomic.Int32
	hub := NewHub(HubOptions{
		Authenticate: func(context.Context, string, string) (Identity, error) { return id, nil },
		OwnerEpoch:   func(context.Context) (uint64, error) { return 2, nil },
		Heartbeat: func(ctx context.Context, _ Identity, _ string, _ uint64, _ Health) error {
			if heartbeats.Add(1) == 1 {
				return nil
			}
			if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > callbackTimeout {
				t.Error("heartbeat callback unbounded")
			}
			close(entered)
			<-ctx.Done()
			return nil
		},
	})
	server := httptest.NewServer(hub)
	defer server.Close()
	defer hub.Close()
	conn := connectRawNode(t, server.URL, id)
	defer conn.Close()
	wait(t, func() bool { return hub.Online(id.NodeID) })
	hub.mu.Lock()
	connection := hub.peers[id.NodeID].id
	hub.mu.Unlock()
	if err := writeFrame(conn, frame{Type: "heartbeat", ConnectionID: connection, Health: &Health{ProviderReady: true}}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("heartbeat not entered")
	}
	assertPrompt(t, func() { hub.Disconnect(id.NodeID) })
	if _, err := readFrame(conn); err == nil {
		t.Fatal("canceled heartbeat was acknowledged")
	}
	wait(t, func() bool { return reservationReleased(hub, id.NodeID) })
	if hub.Online(id.NodeID) {
		t.Fatal("disconnected heartbeat remained published")
	}
}
