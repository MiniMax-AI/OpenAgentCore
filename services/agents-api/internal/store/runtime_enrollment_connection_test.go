package store_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/execution"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/runtime"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/runtimeenrollment"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

func TestEnrolledDaemonConnectionRevocationAndRestart(t *testing.T) {
	s, pool := store.NewTestStore(t)
	principal := store.FixtureExecutorPrincipal(t, s, uuid.NewString())
	session, err := s.CreateSession(t.Context(), principal.TenantID, store.CreateSessionInput{
		Creator: principal.Subject(), Engine: "codex", IdempotencyKey: uuid.NewString(),
		Configuration: json.RawMessage(`{"agent":{"model":"fixture"},"environment":{"type":"self_hosted","workspace_directory":"/workspace"}}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	environment, err := s.GetSessionEnvironment(t.Context(), principal.TenantID, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	key, err := s.IssueExecutorCredential(t.Context(), principal, uuid.NewString(), environment.ID)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(key.Token))
	bound, err := s.EnrollRuntime(t.Context(), environment.ID, hex.EncodeToString(digest[:]))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(nil)
	wsURL := "ws://" + server.Listener.Addr().String() + "/api/v1/agent-daemon/ws"
	handler, registry, err := runtime.NewGateway(s, wsURL)
	if err != nil {
		t.Fatal(err)
	}
	connection := runtimeenrollment.ConnectionHandler(s, registry)
	assertConnection := func(target, token, status string, code int) {
		t.Helper()
		request := httptest.NewRequest("GET", "/api/v1/agent-daemon/connection?environment_id="+target, nil)
		request.Header.Set("Authorization", "Bearer "+token)
		response := httptest.NewRecorder()
		connection.ServeHTTP(response, request)
		if response.Code != code || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("connection status %d, want %d", response.Code, code)
		}
		if code == 200 {
			var got map[string]string
			if json.Unmarshal(response.Body.Bytes(), &got) != nil || len(got) != 2 || got["environment_id"] != target || got["status"] != status {
				t.Fatalf("connection response: %s", response.Body.String())
			}
		}
	}
	assertConnection(environment.ID, key.Token, "disconnected", 200)
	assertConnection(uuid.NewString(), key.Token, "", 401)
	otherKey, err := s.IssueExecutorCredential(t.Context(), principal, uuid.NewString(), environment.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertConnection(environment.ID, otherKey.Token, "", 409)
	foreign := store.FixtureExecutorPrincipal(t, s, uuid.NewString())
	foreignKey, err := s.IssueExecutorCredential(t.Context(), foreign, uuid.NewString(), "")
	if err != nil {
		t.Fatal(err)
	}
	assertConnection(environment.ID, foreignKey.Token, "", 401)
	server.Config.Handler = handler
	server.Start()
	t.Cleanup(func() { server.Close(); runtime.CloseConnections(registry) })
	start := func() func() {
		worker, err := execution.StartWorker(t.Context(), &execution.Dispatcher{Store: s, Registry: registry})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- worker.Run(ctx) }()
		return func() {
			cancel()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("worker did not stop")
			}
		}
	}
	stop := start()
	defer func() {
		if stop != nil {
			stop()
		}
	}()
	connect := func(token string) *websocket.Conn {
		u, _ := url.Parse(wsURL)
		u.RawQuery = url.Values{"device_id": {bound.DeviceID}, "version": {proto.Version}}.Encode()
		conn, resp, err := websocket.DefaultDialer.Dial(u.String(), http.Header{"Authorization": {"Bearer " + token}})
		if resp != nil {
			resp.Body.Close()
		}
		if err != nil {
			t.Fatal("daemon connection rejected")
		}
		t.Cleanup(func() { conn.Close() })
		return conn
	}
	await := func(status string) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			current, err := s.GetEnvironment(t.Context(), principal.TenantID, environment.ID)
			if err == nil && current.Status == status {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("environment did not become %s", status)
	}
	first := connect(key.Token)
	await("connected")
	assertConnection(environment.ID, key.Token, "connected", 200)
	rotated, err := s.RotateExecutorCredential(t.Context(), principal, key.KeyID)
	if err != nil {
		t.Fatal(err)
	}
	assertConnection(environment.ID, key.Token, "", 401)
	assertConnection(environment.ID, rotated.Token, "disconnected", 200)
	// No heartbeat is sent: the Worker's authority check must fence the old socket.
	await("disconnected")
	_ = first.SetReadDeadline(time.Now().Add(time.Second))
	if _, _, err = first.ReadMessage(); err == nil {
		t.Fatal("rotated socket retained authority")
	}
	second := connect(rotated.Token)
	await("connected")
	assertConnection(environment.ID, rotated.Token, "connected", 200)
	stop()
	stop = nil
	// A new Core owner clears prior transport evidence, then observes the same
	// live, authorized daemon. No compute allocation or native execution is made.
	stop = start()
	await("connected")
	if err = s.RevokeExecutorCredential(t.Context(), principal, key.KeyID); err != nil {
		t.Fatal(err)
	}
	assertConnection(environment.ID, rotated.Token, "", 401)
	await("disconnected")
	_ = second.SetReadDeadline(time.Now().Add(time.Second))
	if _, _, err = second.ReadMessage(); err == nil {
		t.Fatal("revoked socket retained authority")
	}
	var allocations int
	if err = pool.QueryRow(t.Context(), "SELECT count(*) FROM runtime_allocations WHERE environment_id=$1", environment.ID).Scan(&allocations); err != nil || allocations != 0 {
		t.Fatal("user Runtime acquired managed allocation", allocations, err)
	}
	current, err := s.GetSession(t.Context(), principal.TenantID, session.ID)
	if err != nil || current.LastTurn != nil {
		t.Fatal("connection handling created execution", err)
	}
}
