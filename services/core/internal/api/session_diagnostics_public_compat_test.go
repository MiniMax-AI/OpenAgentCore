package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/identity"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/sessionpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/migrations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Fixed resource times make the logged public response bytes comparable
// between revisions.
func TestDiagnosticPublicCompatibility(t *testing.T) {
	s, pool := diagnosticDatabase(t)
	key := callerBinding()
	deps, fakes := testDependencies(t)
	fakes.projectsReader.resolveAPIKey = projectKeys(t, key).ResolveAPIKey
	databaseSessionReads(s, pool)(&deps, fakes)
	h := newTestHandler(t, deps)
	session, err := s.CreateSession(t.Context(), key.TenantID, sessions.CreateSession{Creator: identity.Subject{Kind: "service_account", ID: "compat-test"}, Engine: "codex", IdempotencyKey: uuid.NewString(), Configuration: json.RawMessage(`{"agent":{"id":"agent_root","model":"test"},"environment":{"type":"none"}}`)})
	if err != nil {
		t.Fatal(err)
	}
	turn := uuid.NewString()
	item := uuid.NewString()
	if _, err = pool.Exec(t.Context(), "UPDATE sessions SET created_at='2026-09-28T00:00:00Z' WHERE id=$1", session.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(t.Context(), `INSERT INTO turns(id,session_id,status,created_at,started_at,completed_at,outcome) VALUES($1,$2,'failed','2026-09-28T00:00:00Z','2026-09-28T00:00:01Z','2026-09-28T00:00:02Z','{"error_code":"engine_failed","error":"private-secret-canary"}')`, turn, session.ID); err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]any{"id": item, "turn_id": turn, "type": "command_execution", "status": "failed", "command": "example", "exit_code": 1, "duration_ms": 5})
	if _, err = pool.Exec(t.Context(), `INSERT INTO session_items(id,session_id,turn_id,created_at,payload) VALUES($1,$2,$3,'2026-09-28T00:00:01Z',$4)`, item, session.ID, turn, payload); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"", "/turns/" + turn, "/items"} {
		w := diagnosticRequest(h, "/v1/agents/sessions/"+session.ID+suffix, "Bearer caller")
		if w.Code != 200 || strings.Contains(w.Body.String(), "canary") || strings.Contains(w.Body.String(), "observed_duration") || strings.Contains(w.Body.String(), "failure_detail") {
			t.Fatal(w.Code, w.Body)
		}
		body := strings.NewReplacer(session.ID, "SESSION", turn, "TURN", item, "ITEM").Replace(w.Body.String())
		t.Logf("PUBLIC %s %s", strings.ReplaceAll(suffix, turn, "TURN"), strings.TrimSpace(body))
	}
}

func diagnosticRequest(handler http.Handler, path, token string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, path, nil)
	r.Header.Set("Authorization", token)
	r.Header.Set("OpenAI-Beta", "agents=v1")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w
}

// databaseSessionReads serves Session, Turn and diagnostic reads from s, and
// Item reads from the Session adapter on pool.
func databaseSessionReads(s *store.Store, pool *pgxpool.Pool) func(*Dependencies, *testFakes) {
	return func(d *Dependencies, _ *testFakes) {
		d.Sessions, d.Turns, d.SessionAdmin = s, s, s
		d.Items = sessionpg.New(pgunit.NewPool(pool))
	}
}

func diagnosticDatabase(t *testing.T) (*store.Store, *pgxpool.Pool) {
	t.Helper()
	dsn := os.Getenv("OAC_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("OAC_TEST_DATABASE_URL is not set; dedicated PostgreSQL required")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal("invalid test database configuration")
	}
	if !strings.HasPrefix(cfg.ConnConfig.Database, "oac_") || !strings.HasSuffix(cfg.ConnConfig.Database, "_tests") {
		t.Fatal("test database must be named oac_*_tests")
	}
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	var productTable *string
	if err = pool.QueryRow(t.Context(), "SELECT to_regclass('workspaces')::text").Scan(&productTable); err != nil || productTable != nil {
		t.Fatal("dedicated Core database required", err)
	}
	if err = migrations.Apply(t.Context(), dsn); err != nil {
		t.Fatal(err)
	}
	return store.New(pool), pool
}
