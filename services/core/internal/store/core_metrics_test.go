package store

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

func coreMetricsSession(t *testing.T, pool *pgxpool.Pool, deleted bool) string {
	t.Helper()
	id := uuid.NewString()
	_, err := pool.Exec(t.Context(), `INSERT INTO sessions(id,tenant_id,engine,idempotency_key,request_hash,deleted_at)
		VALUES($1,$2,'codex','metrics','metrics',CASE WHEN $3::boolean THEN clock_timestamp() END)`, id, uuid.NewString(), deleted)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, query := range []string{`DELETE FROM turns WHERE session_id=$1`, `DELETE FROM session_devices WHERE session_id=$1`, `DELETE FROM sessions WHERE id=$1`} {
			if _, err := pool.Exec(context.Background(), query, id); err != nil {
				t.Error(err)
			}
		}
	})
	return id
}

func coreMetricsTurn(t *testing.T, pool *pgxpool.Pool, session, status, code string, created time.Time, started, completed *time.Time) {
	t.Helper()
	_, err := pool.Exec(t.Context(), `INSERT INTO turns(id,session_id,status,created_at,started_at,completed_at,outcome)
		VALUES($1,$2,$3,$4,$5,$6,jsonb_build_object('error_code',$7::text))`, uuid.NewString(), session, status, created, started, completed, code)
	if err != nil {
		t.Fatal(err)
	}
}

func TestCoreMetricsSnapshot(t *testing.T) {
	s, pool := testStore(t)
	now := time.Now().UTC()
	baseline, err := s.ReadCoreExecutionSnapshot(t.Context(), now, []string{})
	if err != nil {
		t.Fatal(err)
	}
	if baseline.QueuedTurns == 0 && baseline.OldestQueuedSeconds != nil {
		t.Fatal("empty queue must not invent an age")
	}
	connected := uuid.NewString()
	if _, err := pool.Exec(t.Context(), `INSERT INTO devices(id,tenant_id,name,credential_hash) VALUES($1,$2,'metrics',$3)`, connected, uuid.NewString(), strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM devices WHERE id=$1`, connected) })
	oldest := time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC)
	for i, status := range []string{"queued", "queued", "queued", "in_progress", "waiting"} {
		session := coreMetricsSession(t, pool, false)
		coreMetricsTurn(t, pool, session, status, "", oldest.Add(time.Duration(i)*time.Second), nil, nil)
		if i == 0 {
			if _, err := pool.Exec(t.Context(), `INSERT INTO session_devices(session_id,device_id) VALUES($1,$2)`, session, connected); err != nil {
				t.Fatal(err)
			}
		}
	}
	got, err := s.ReadCoreExecutionSnapshot(t.Context(), now, []string{connected})
	if err != nil {
		t.Fatal(err)
	}
	if got.QueuedTurns != baseline.QueuedTurns+3 || got.InProgressTurns != baseline.InProgressTurns+1 || got.WaitingForDaemon != baseline.WaitingForDaemon+2 {
		t.Fatalf("unexpected snapshot: %+v; baseline %+v", got, baseline)
	}
	if got.OldestQueuedSeconds == nil || math.Abs(*got.OldestQueuedSeconds-now.Sub(oldest).Seconds()) > 0.001 {
		t.Fatalf("oldest age: %v", got.OldestQueuedSeconds)
	}
	disconnected, err := s.ReadCoreExecutionSnapshot(t.Context(), now, []string{})
	if err != nil || disconnected.WaitingForDaemon != got.WaitingForDaemon+1 {
		t.Fatalf("registry disconnect: %+v, %v", disconnected, err)
	}
	if _, err := s.ReadCoreExecutionSnapshot(t.Context(), now, []string{"invalid-device-id"}); err == nil {
		t.Fatal("invalid registry ID accepted")
	}
}

func TestCoreMetricsHistory(t *testing.T) {
	s, pool := testStore(t)
	start := time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(3 * time.Minute)
	// Boundary samples exercise inclusive start, exclusive end and exact buckets.
	for i, offset := range []time.Duration{-time.Second, 0, 30 * time.Second, time.Minute, 3 * time.Minute} {
		started := start.Add(offset)
		wait := []time.Duration{9 * time.Second, 0, time.Second, 3 * time.Second, 9 * time.Second}[i]
		completed := started.Add(time.Second)
		if i == 4 {
			completed = end
		}
		coreMetricsTurn(t, pool, coreMetricsSession(t, pool, i == 2), "failed", "execution_interrupted", started.Add(-wait), &started, &completed)
	}
	// Nonmatching outcomes, nonfailed status and a queued never-started Turn do
	// not contribute an interruption; cancellation without a start is no wait.
	for _, status := range []string{"completed", "cancelled", "failed"} {
		completed := start.Add(20 * time.Second)
		code := "execution_interrupted"
		if status == "failed" {
			code = "other_error"
		}
		coreMetricsTurn(t, pool, coreMetricsSession(t, pool, false), status, code, start, nil, &completed)
	}
	got, err := s.ReadCoreExecutionHistory(t.Context(), start, end, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if got.Interrupted != 4 || len(got.Buckets) != 3 {
		t.Fatalf("history counts: %+v", got)
	}
	check := func(label string, got *float64, want float64) {
		t.Helper()
		if got == nil || math.Abs(*got-want) > 0.001 {
			t.Fatalf("%s: got %v, want %v", label, got, want)
		}
	}
	check("range p50", got.QueueWaitMS.P50, 1000)
	check("range p95", got.QueueWaitMS.P95, 2800)
	check("first bucket p95", got.Buckets[0].P95MS, 950)
	check("second bucket p95", got.Buckets[1].P95MS, 3000)
	if got.Buckets[2].P95MS != nil || !got.Buckets[2].Start.Equal(start.Add(2*time.Minute)) {
		t.Fatal("missing bucket must have aligned start and unknown percentile")
	}
	empty, err := s.ReadCoreExecutionHistory(t.Context(), end.Add(time.Hour), end.Add(2*time.Hour), time.Minute)
	if err != nil || empty.Interrupted != 0 || empty.QueueWaitMS.P50 != nil || empty.QueueWaitMS.P95 != nil || len(empty.Buckets) != 60 {
		t.Fatalf("empty history: %+v, %v", empty, err)
	}
	for _, bucket := range empty.Buckets {
		if bucket.P95MS != nil {
			t.Fatal("empty bucket invented zero percentile")
		}
	}
	size, err := s.ReadCoreDatabaseSize(t.Context())
	if err != nil || size <= 0 {
		t.Fatalf("database size: %d, %v", size, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := s.ReadCoreExecutionSnapshot(ctx, start, nil); err == nil {
		t.Fatal("snapshot read error hidden")
	}
	if result, err := s.ReadCoreExecutionHistory(ctx, start, end, time.Minute); err == nil || result.Buckets != nil {
		t.Fatal("history read error hidden or partial data returned")
	}
	if _, err := s.ReadCoreDatabaseSize(ctx); err == nil {
		t.Fatal("database size read error hidden")
	}
}

func TestCoreMetricsHistoryBounds(t *testing.T) {
	s := &Store{}
	start := time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		start, end time.Time
		step       time.Duration
	}{
		{start, start, time.Minute},
		{start, start.Add(time.Hour), 0},
		{start, start.Add(time.Hour), 1500 * time.Millisecond},
		{start, start.Add(8 * 24 * time.Hour), time.Hour},
		{start, start.Add(time.Hour), time.Second},
		{start.Add(time.Second), start.Add(time.Hour + time.Second), time.Minute},
		{start, start.Add(time.Hour + time.Second), time.Minute},
		{start.Add(time.Nanosecond), start.Add(time.Hour + time.Nanosecond), time.Minute},
	} {
		if _, err := s.ReadCoreExecutionHistory(t.Context(), tc.start, tc.end, tc.step); !errors.Is(err, sessions.ErrInvalidInput) {
			t.Fatalf("unbounded or unaligned range accepted: %+v, %v", tc, err)
		}
	}
}
