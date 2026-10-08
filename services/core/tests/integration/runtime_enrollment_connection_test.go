package integration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxlink"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxlink/sandboxlinktest"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/execution"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgtest"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimeenrollment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimegateway"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/google/uuid"
)

// A self_hosted Environment is connected while its enrollment is Serving. A
// rotation or revocation ends the old secret's Serve through the Worker's
// revocation pass, and a restarted Worker observes the same Serve.
func TestEnrolledSandboxConnectionRevocationAndRestart(t *testing.T) {
	s, _ := testStore(t)
	principal := FixtureExecutorPrincipal(t, s, uuid.NewString())
	session, err := s.CreateSession(t.Context(), principal.TenantID, sessions.CreateSession{
		Creator: principal.Subject(), Engine: "codex", IdempotencyKey: uuid.NewString(),
		Configuration: json.RawMessage(`{"agent":{"model":"fixture"},"environment":{"type":"self_hosted","workspace_directory":"/workspace"}}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	environment, err := sessionAdapter(s).GetSessionEnvironment(t.Context(), principal.TenantID, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	key, err := sessionService(t, s).IssueExecutorCredential(t.Context(), principal, uuid.NewString(), environment.ID)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(key.Token))
	bound, err := sessionService(t, s).EnrollRuntime(t.Context(), environment.ID, hex.EncodeToString(digest[:]))
	if err != nil {
		t.Fatal(err)
	}
	link := sandboxlinktest.StartRelay(t, runtimegateway.NewLinkAuthority(sessionAdapter(s)))
	connection := &runtimeenrollment.Connections{Store: sessionAdapter(s), Links: link.Relay}
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
	otherKey, err := sessionService(t, s).IssueExecutorCredential(t.Context(), principal, uuid.NewString(), environment.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertConnection(environment.ID, otherKey.Token, "", 409)
	foreign := FixtureExecutorPrincipal(t, s, uuid.NewString())
	foreignKey, err := sessionService(t, s).IssueExecutorCredential(t.Context(), foreign, uuid.NewString(), "")
	if err != nil {
		t.Fatal(err)
	}
	assertConnection(environment.ID, foreignKey.Token, "", 401)
	start := func() func() {
		worker := startWorker(t, t.Context(), s, &execution.Dispatcher{Registry: runtimegateway.NewRegistry(), Links: link.Relay})
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
	await := func(status string) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			current, err := sessionAdapter(s).GetEnvironment(t.Context(), principal.TenantID, environment.ID)
			if err == nil && current.Status == status {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("environment did not become %s", status)
	}
	first := startLinkServe(t, link, []byte(key.Token), bound.Ref())
	within(t, first.connected)
	await("connected")
	assertConnection(environment.ID, key.Token, "connected", 200)
	rotated, err := sessionService(t, s).RotateExecutorCredential(t.Context(), principal, key.KeyID)
	if err != nil {
		t.Fatal(err)
	}
	assertConnection(environment.ID, key.Token, "", 401)
	assertConnection(environment.ID, rotated.Token, "disconnected", 200)
	// The Worker's pass revokes the previous generation, which closes the old
	// secret's Serve; its redial is refused.
	await("disconnected")
	if got := first.refused(t); got != sandboxlink.AuthenticationFailed {
		t.Fatal("the rotated-out secret's Serve ended with", got)
	}
	next := bound
	next.Generation++
	second := startLinkServe(t, link, []byte(rotated.Token), next.Ref())
	within(t, second.connected)
	await("connected")
	assertConnection(environment.ID, rotated.Token, "connected", 200)
	awaitRelease := pgtest.ObserveExecutionLeaseRelease(t, s.pool)
	stop()
	stop = nil
	awaitRelease()
	// A new Core owner clears prior connection evidence, then observes the
	// same Serving resource. No compute allocation or native execution is made.
	stop = start()
	await("connected")
	if err = sessionService(t, s).RevokeExecutorCredential(t.Context(), principal, key.KeyID); err != nil {
		t.Fatal(err)
	}
	assertConnection(environment.ID, rotated.Token, "", 401)
	await("disconnected")
	if got := second.refused(t); got != sandboxlink.AuthenticationFailed {
		t.Fatal("the revoked key's Serve ended with", got)
	}
	var allocations int
	if err = s.pool.QueryRow(t.Context(), "SELECT count(*) FROM runtime_allocations WHERE environment_id=$1", environment.ID).Scan(&allocations); err != nil || allocations != 0 {
		t.Fatal("self_hosted Environment acquired a managed allocation", allocations, err)
	}
	current, err := sessionAdapter(s).GetSession(t.Context(), principal.TenantID, session.ID)
	if err != nil || current.LastTurn != nil {
		t.Fatal("connection handling created execution", err)
	}
}
