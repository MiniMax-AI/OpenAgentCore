package node

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"sync"
)

func TestCapacityRefreshUsesAuthenticatedCoreIdentity(t *testing.T) {
	id := identity()
	var stored StoredIdentity
	approved := EnrollmentResponse{NodeID: id.NodeID, InstallationID: id.InstallationID, Provider: id.Provider, MaxActive: 2, MaxRetained: 8}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/sandbox-node/identity" || r.URL.Query().Get("node_id") != id.NodeID || r.Header.Get("Authorization") != "Bearer "+stored.Credential {
			t.Error("identity refresh lacked retained authority")
			w.WriteHeader(401)
			return
		}
		_ = json.NewEncoder(w).Encode(approved)
	}))
	defer server.Close()
	dir := stateDir(t)
	var err error
	stored, err = InitIdentity(dir, server.URL, id)
	if err != nil {
		t.Fatal(err)
	}
	refreshed, err := RefreshIdentity(t.Context(), dir)
	if err != nil || refreshed.Identity.MaxActive != 2 || refreshed.Identity.MaxRetained != 8 || refreshed.Credential != stored.Credential {
		t.Fatal("approved capacity not refreshed", err)
	}
	persisted, err := LoadIdentity(dir)
	if err != nil || persisted != refreshed {
		t.Fatal("approved capacity not persisted", err)
	}
	approved.InstallationID = "another-installation"
	if _, err = RefreshIdentity(t.Context(), dir); err == nil {
		t.Fatal("foreign identity accepted")
	}
	after, _ := LoadIdentity(dir)
	if after != persisted {
		t.Fatal("rejected response changed retained identity")
	}
}

func TestHubCapacityCacheCannotOverrideAdminChangesOrBlockReconnect(t *testing.T) {
	id := identity()
	approved := id
	var mu sync.Mutex
	hub := NewHub(HubOptions{Authenticate: func(context.Context, string, string) (Identity, error) {
		mu.Lock()
		defer mu.Unlock()
		return approved, nil
	}, OwnerEpoch: func(context.Context) (uint64, error) { return 1, nil }})
	defer hub.Close()
	server := httptest.NewServer(hub)
	defer server.Close()
	cached := id
	cached.MaxActive, cached.MaxRetained = 100, 100
	conn := connectRawNode(t, server.URL, cached)
	defer conn.Close()
	wait(t, func() bool { return hub.Online(id.NodeID) })
	hub.mu.Lock()
	peer := hub.peers[id.NodeID]
	hub.mu.Unlock()
	if peer.identity.MaxActive != id.MaxActive {
		t.Fatal("node cache overrode authenticated capacity")
	}
	mu.Lock()
	approved.MaxActive, approved.MaxRetained = 2, 8
	mu.Unlock()
	if err := writeFrame(conn, frame{Type: "heartbeat", ConnectionID: peer.id, Health: &Health{ProviderReady: true}}); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	reply, err := readFrame(conn)
	if err != nil || reply.Type != "heartbeat_ack" {
		t.Fatal("admin capacity change interrupted node", err)
	}
	conn.Close()
	wait(t, func() bool { return reservationReleased(hub, id.NodeID) })
	reconnect := connectRawNode(t, server.URL, cached)
	defer reconnect.Close()
	wait(t, func() bool { return hub.Online(id.NodeID) })
	hub.mu.Lock()
	peer = hub.peers[id.NodeID]
	hub.mu.Unlock()
	if peer.identity.MaxActive != 2 || peer.identity.MaxRetained != 8 {
		t.Fatal("reconnect did not use current approved capacity")
	}
}
