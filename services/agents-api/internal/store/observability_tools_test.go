package store

import (
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/observability"
	"github.com/google/uuid"
)

func TestPostgresToolAttemptIsIdempotentAndTenantScoped(t *testing.T) {
	s, pool := testStore(t)
	tenant, session, turn, attemptID := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	_, err := pool.Exec(t.Context(), `INSERT INTO sessions (id, tenant_id, engine, idempotency_key, request_hash)
		VALUES ($1, $2, 'codex', $3, 'observability-test')`, session, tenant, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(t.Context(), `DELETE FROM turns WHERE id=$1`, turn)
		_, _ = pool.Exec(t.Context(), `DELETE FROM sessions WHERE id=$1`, session)
	})
	_, err = pool.Exec(t.Context(), `INSERT INTO turns (id, session_id, status, completed_at)
		VALUES ($1, $2, 'completed', clock_timestamp())`, turn, session)
	if err != nil {
		t.Fatal(err)
	}
	finished := time.Now().UTC()
	duration := int64(120)
	value := observability.ToolAttempt{ID: attemptID, TenantID: tenant, SessionID: session, TurnID: turn,
		FinishedAt: finished, Category: "command", Outcome: "success", DurationMS: &duration}
	if err := s.WriteToolAttempts(t.Context(), []observability.ToolAttempt{value}, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteToolAttempts(t.Context(), []observability.ToolAttempt{value}, 0, 0); err != nil {
		t.Fatal(err)
	}
	var count int
	var startedAt *time.Time
	if err := pool.QueryRow(t.Context(), `SELECT count(*), max(started_at) FROM observability_tool_attempts WHERE id=$1`, attemptID).Scan(&count, &startedAt); err != nil || count != 1 || startedAt != nil {
		t.Fatalf("tool attempt deduplication or unknown start failed: count=%d start=%v err=%v", count, startedAt, err)
	}
	value.ID = uuid.NewString()
	value.TenantID = uuid.NewString()
	if err := s.WriteToolAttempts(t.Context(), []observability.ToolAttempt{value}, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM observability_tool_attempts WHERE id=$1`, value.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("foreign tenant wrote an attempt: count=%d err=%v", count, err)
	}
	metrics, err := s.ReadOperatorMetrics(t.Context(), finished.Add(-time.Minute), finished.Add(time.Minute), time.Minute)
	if err != nil || len(metrics.Tools) == 0 || len(metrics.Turns) == 0 {
		t.Fatalf("terminal operator metrics unavailable: tools=%+v turns=%+v err=%v", metrics.Tools, metrics.Turns, err)
	}
}
