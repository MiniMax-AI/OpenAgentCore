package node

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

type fakeProvider struct {
	mu                    sync.Mutex
	creates, kills, reads int
	started               chan struct{}
	release               chan struct{}
}

func (p *fakeProvider) Create(ctx context.Context, b sandbox.Bootstrap) (sandbox.Info, error) {
	p.mu.Lock()
	p.creates++
	p.mu.Unlock()
	if p.started != nil {
		close(p.started)
	}
	if p.release != nil {
		<-p.release
	}
	return sandbox.Info{Reference: b.Reference, ProviderID: "compute", State: "running", BootstrapComplete: true}, nil
}
func (p *fakeProvider) GetInfo(ctx context.Context, r sandbox.Reference) (sandbox.Info, error) {
	p.mu.Lock()
	p.reads++
	p.mu.Unlock()
	return sandbox.Info{Reference: r, ProviderID: "compute", State: "running", BootstrapComplete: true}, nil
}
func (p *fakeProvider) Renew(ctx context.Context, r sandbox.Reference) (sandbox.Info, error) {
	return p.GetInfo(ctx, r)
}
func (p *fakeProvider) Kill(context.Context, sandbox.Reference) error {
	p.mu.Lock()
	p.kills++
	p.mu.Unlock()
	return nil
}
func (p *fakeProvider) RunCommand(context.Context, sandbox.Reference, sandbox.Command) (sandbox.CommandResult, error) {
	return sandbox.CommandResult{}, nil
}
func reference() sandbox.Reference {
	return sandbox.Reference{TenantID: uuid.NewString(), EnvironmentID: uuid.NewString(), AllocationID: uuid.NewString()}
}
func identity() Identity {
	return Identity{NodeID: uuid.NewString(), InstallationID: uuid.NewString(), Provider: "docker", BackendFingerprint: strings.Repeat("1", 64), MaxActive: 4, MaxRetained: 16}
}
func stateDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "state")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	return dir
}
func wait(t *testing.T, ready func() bool) {
	t.Helper()
	until := time.Now().Add(5 * time.Second)
	for !ready() {
		if time.Now().After(until) {
			t.Fatal("condition not reached")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
func probe(context.Context) (Health, error) { return Health{ProviderReady: true}, nil }

func TestLostCreateResponseDoesNotReplayAndReconnectSerializesCleanup(t *testing.T) {
	id := identity()
	var credential string
	hub := NewHub(HubOptions{Authenticate: func(ctx context.Context, node, token string) (Identity, error) {
		if node != id.NodeID || token != credential {
			return Identity{}, ErrAuthentication
		}
		return id, nil
	}, OwnerEpoch: func(context.Context) (uint64, error) { return 7, nil }})
	server := httptest.NewServer(hub)
	defer server.Close()
	defer hub.Close()
	dir := stateDir(t)
	stored, err := InitIdentity(dir, server.URL, id)
	if err != nil {
		t.Fatal(err)
	}
	credential = stored.Credential
	p := &fakeProvider{started: make(chan struct{}), release: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, AgentConfig{CoreURL: server.URL, StateDirectory: dir, Identity: id, Credential: credential, Provider: p, Probe: probe})
	}()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("agent did not stop")
		}
	}()
	wait(t, func() bool { return hub.Online(id.NodeID) })
	if _, err := LoadIdentity(dir); err == nil {
		t.Fatal("running node did not retain lifetime identity lock")
	}
	r := reference()
	proxy := hub.Proxy(id.NodeID, "docker")
	createCtx, stopCreate := context.WithTimeout(ctx, 150*time.Millisecond)
	defer stopCreate()
	createDone := make(chan error, 1)
	go func() { _, err := proxy.Create(createCtx, sandbox.Bootstrap{Reference: r}); createDone <- err }()
	<-p.started
	if err := <-createDone; !errors.Is(err, sandbox.ErrComputeUnconfirmed) {
		t.Fatalf("lost reply = %v", err)
	}
	hub.mu.Lock()
	previous := hub.peers[id.NodeID]
	hub.mu.Unlock()
	hub.Disconnect(id.NodeID)
	wait(t, func() bool {
		hub.mu.Lock()
		defer hub.mu.Unlock()
		return hub.peers[id.NodeID] != nil && hub.peers[id.NodeID] != previous
	})
	cleanupCtx, stopCleanup := context.WithTimeout(ctx, 3*time.Second)
	defer stopCleanup()
	cleaned := make(chan error, 1)
	go func() { cleaned <- proxy.Kill(cleanupCtx, r) }()
	time.Sleep(50 * time.Millisecond)
	p.mu.Lock()
	kills := p.kills
	creates := p.creates
	p.mu.Unlock()
	if kills != 0 || creates != 1 {
		t.Fatalf("overlap/replay before create settled: creates=%d kills=%d", creates, kills)
	}
	close(p.release)
	if err := <-cleaned; err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.creates != 1 || p.kills != 1 {
		t.Fatalf("unexpected effects creates=%d kills=%d", p.creates, p.kills)
	}
}

func TestOfflineIsUnknownAndDockerDoesNotAdvertiseCheckpoint(t *testing.T) {
	h := NewHub(HubOptions{OwnerEpoch: func(context.Context) (uint64, error) { return 1, nil }})
	p := h.Proxy(uuid.NewString(), "docker")
	if _, ok := p.(sandbox.CheckpointProvider); ok {
		t.Fatal("docker advertised checkpoint")
	}
	_, err := p.GetInfo(context.Background(), reference())
	if !errors.Is(err, sandbox.ErrComputeUnconfirmed) || errors.Is(err, sandbox.ErrNotFound) {
		t.Fatalf("offline = %v", err)
	}
}

func TestAgentRejectsDuplicateSequenceAndRetainsEpoch(t *testing.T) {
	id := identity()
	p := &fakeProvider{}
	upgrade := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, e := upgrade.Upgrade(w, r, nil)
		if e != nil {
			return
		}
		defer conn.Close()
		conn.SetReadLimit(MaxFrameBytes)
		if _, e = readFrame(conn); e != nil {
			return
		}
		connectionID := uuid.NewString()
		_ = writeFrame(conn, frame{Type: "welcome", ConnectionID: connectionID, OwnerEpoch: 9})
		q := request{ID: uuid.NewString(), Sequence: 1, ConnectionID: connectionID, OwnerEpoch: 9, Operation: "info", TimeoutMillis: 60000, Reference: reference()}
		_ = writeFrame(conn, frame{Type: "request", Request: &q})
		_, _ = readFrame(conn)
		q.ID = uuid.NewString()
		_ = writeFrame(conn, frame{Type: "request", Request: &q})
		_, _ = readFrame(conn)
	}))
	defer server.Close()
	dir := stateDir(t)
	stored, e := InitIdentity(dir, server.URL, id)
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := Run(ctx, AgentConfig{CoreURL: server.URL, StateDirectory: dir, Identity: id, Credential: stored.Credential, Provider: p, Probe: probe})
	if !errors.Is(err, sandbox.ErrOwnership) {
		t.Fatalf("duplicate sequence = %v", err)
	}
	p.mu.Lock()
	reads := p.reads
	p.mu.Unlock()
	if reads != 1 {
		t.Fatalf("replayed operation %d", reads)
	}
	persisted, e := LoadIdentity(dir)
	if e != nil || persisted.OwnerEpoch != 9 {
		t.Fatalf("epoch not retained: %+v %v", persisted.Identity, e)
	}
}

func TestEnrollmentLostResponseRecoversWithPersistedCredential(t *testing.T) {
	id := identity()
	var stored StoredIdentity
	enrollments := 0
	registered := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/core/v1/sandbox/node/identity" {
			if !registered || r.Header.Get("Authorization") != "Bearer "+stored.Credential {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_ = json.NewEncoder(w).Encode(EnrollmentResponse{NodeID: id.NodeID, InstallationID: id.InstallationID, Provider: id.Provider})
			return
		}
		if r.URL.Path != "/core/v1/sandbox/enroll" || r.Header.Get("Authorization") != "Bearer enrollment" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var body EnrollmentRequest
		if json.NewDecoder(r.Body).Decode(&body) != nil || body.Credential != stored.Credential || body.NodeID != id.NodeID {
			t.Error("unpersisted credential used")
		}
		enrollments++
		registered = true
		hijacker := w.(http.Hijacker)
		conn, _, err := hijacker.Hijack()
		if err == nil {
			_ = conn.Close()
		}
	}))
	defer server.Close()
	dir := stateDir(t)
	var err error
	stored, err = InitIdentity(dir, server.URL, id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Enroll(context.Background(), server.URL, dir, "enrollment", EnrollmentRequest{Name: "test"}); err == nil {
		t.Fatal("lost response reported success")
	}
	recovered, err := Enroll(context.Background(), server.URL, dir, "enrollment", EnrollmentRequest{Name: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if enrollments != 1 || recovered.Credential != stored.Credential {
		t.Fatal("enrollment was replayed or identity rotated")
	}
}

func TestCoreURLRejectsRemotePlaintextAndCredentials(t *testing.T) {
	for _, raw := range []string{"http://example.com", "https://user:pass@example.com", "https://example.com/?token=x", "https://example.com/path"} {
		if _, err := endpoint(raw, "/core/v1/sandbox/enroll"); err == nil {
			t.Fatalf("accepted %q", raw)
		}
	}
	for _, raw := range []string{"https://core.example.test:9443", "http://127.0.0.1:8080", "http://[::1]:8080"} {
		if _, err := endpoint(raw, "/core/v1/sandbox/enroll"); err != nil {
			t.Fatalf("rejected %q: %v", raw, err)
		}
	}
}
