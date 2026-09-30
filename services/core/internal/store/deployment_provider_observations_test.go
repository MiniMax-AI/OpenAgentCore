package store

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/adminaudit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type providerObservationFixture struct {
	s       *Store
	pool    *pgxpool.Pool
	tenant  string
	input   CreateSessionInput
	session Session
}

func observationAdmin(t *testing.T) context.Context {
	return adminaudit.WithSource(t.Context(), adminaudit.Source{CredentialID: "fixture-admin", RequestID: uuid.NewString(), TraceID: uuid.NewString()})
}
func newProviderObservationFixture(t *testing.T) providerObservationFixture {
	t.Helper()
	s, pool := newManagedTestStore(t)
	provider := FixtureModelProvider("codex")
	if _, err := s.SetDeploymentModelProvider(observationAdmin(t), "codex", v1.ModelConfigurationInput{ModelProvider: *provider, Model: "fixture"}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := s.DeploymentModelProvider(t.Context(), "codex")
	if err != nil {
		t.Fatal(err)
	}
	input := executionProjectionInput("deployment")
	input.Configuration = json.RawMessage(`{"agent":{"model":"frozen-model"},"environment":{"type":"none"}}`)
	input.ModelProvider, input.ModelProviderSource, input.DeploymentProviderRevision = snapshot.Provider, "deployment", snapshot.Revision
	input.ExecutionConfiguration.ModelProvider = v1.ExecutionProviderSelection{Source: "deployment"}
	tenant := uuid.NewString()
	session, err := s.CreateSession(t.Context(), tenant, input)
	if err != nil {
		t.Fatal(err)
	}
	return providerObservationFixture{s, pool, tenant, input, session}
}
func (f providerObservationFixture) terminal(t *testing.T, status, coreCode, nativeCode string) sessions.Turn {
	t.Helper()
	receipt := submitMessage(t, f.s, f.tenant, f.session.ID, uuid.NewString())
	transition(t, f.s, f.tenant, f.session.ID, receipt.TurnID, sessions.TurnQueued, sessions.TurnInProgress)
	outcome, _ := json.Marshal(map[string]string{"error_code": coreCode, "engine_error_code": nativeCode})
	turn, err := f.s.CompleteExecution(t.Context(), f.tenant, f.session.ID, receipt.TurnID, status, outcome, "", receipt.Sequence)
	if err != nil {
		t.Fatal(err)
	}
	return turn
}
func (f providerObservationFixture) observe(t *testing.T, turn sessions.Turn, want int64) {
	t.Helper()
	n, err := f.s.ObserveDeploymentModelProvider(t.Context(), f.tenant, f.session.ID, turn.ID)
	if err != nil || n != want {
		t.Fatalf("observation writes=%d want=%d err=%v", n, want, err)
	}
}
func (f providerObservationFixture) revision(t *testing.T) uuid.UUID {
	t.Helper()
	var rev uuid.UUID
	if err := f.pool.QueryRow(t.Context(), "SELECT deployment_provider_revision FROM session_execution_configuration WHERE session_id=$1", f.session.ID).Scan(&rev); err != nil {
		t.Fatal(err)
	}
	return rev
}
func TestDeploymentObservationSnapshotReplacementAndRetry(t *testing.T) {
	f := newProviderObservationFixture(t)
	original := f.revision(t)
	// Resolution and insertion have independent boundaries: retain the tuple while
	// a PUT replaces the default, then create with that exact earlier tuple.
	before, err := f.s.DeploymentModelProvider(t.Context(), "codex")
	if err != nil {
		t.Fatal(err)
	}
	replacement := *before.Provider
	replacement.APIKey = "different-fixture-key"
	if _, err = f.s.SetDeploymentModelProvider(observationAdmin(t), "codex", v1.ModelConfigurationInput{ModelProvider: replacement, Model: "fixture"}); err != nil {
		t.Fatal(err)
	}
	if _, err = f.pool.Exec(t.Context(), "UPDATE deployment_model_providers SET updated_at='2000-01-01'"); err != nil {
		t.Fatal(err)
	}
	input := f.input
	input.IdempotencyKey = uuid.NewString()
	stale, err := f.s.CreateSession(t.Context(), f.tenant, input)
	if err != nil {
		t.Fatal(err)
	}
	staleFixture := f
	staleFixture.session = stale
	if staleFixture.revision(t) != original {
		t.Fatal("tuple revision changed during insertion")
	}
	frozen, err := f.s.SessionModelExecution(t.Context(), f.tenant, stale.ID)
	if err != nil || frozen == nil || *frozen != *before.Provider {
		t.Fatal("frozen tuple bundle changed", err)
	}
	staleFixture.observe(t, staleFixture.terminal(t, sessions.TurnFailed, "engine_failed", "authentication_error"), 0)
	current, _ := f.s.DeploymentModelProvider(t.Context(), "codex")
	retry := f.input
	retry.ModelProvider = current.Provider
	retry.DeploymentProviderRevision = current.Revision
	replay, err := f.s.CreateSession(t.Context(), f.tenant, retry)
	if err != nil || replay.ID != f.session.ID || f.revision(t) != original {
		t.Fatal("retry replaced frozen metadata", err)
	}
	if err = f.s.DeleteDeploymentModelProvider(observationAdmin(t), "codex"); err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.SetDeploymentModelProvider(observationAdmin(t), "codex", v1.ModelConfigurationInput{ModelProvider: *before.Provider, Model: "fixture"}); err != nil {
		t.Fatal(err)
	}
	recreated, _ := f.s.DeploymentModelProvider(t.Context(), "codex")
	if recreated.Revision == original || recreated.Revision == current.Revision {
		t.Fatal("revision reused")
	}
	frozenProvider, err := f.s.SessionModelExecution(t.Context(), f.tenant, f.session.ID)
	if err != nil || frozenProvider == nil || *frozenProvider != *f.input.ModelProvider {
		t.Fatal("migration/replacement changed Session bundle", err)
	}
	f.observe(t, f.terminal(t, sessions.TurnCompleted, "", ""), 0)
}
func TestDeploymentObservationEligibilityAndReset(t *testing.T) {
	allowed := []string{"authentication_error", "connection_failed", "rate_limit_exceeded", "usage_limit_exceeded", "server_overloaded", "server_error", "resource_not_found", "request_timeout", "invalid_request"}
	f := newProviderObservationFixture(t)
	for _, code := range allowed {
		if _, err := f.pool.Exec(t.Context(), "UPDATE deployment_model_providers SET last_error_at=NULL,last_error_code=NULL,recovery_pending=false"); err != nil {
			t.Fatal(err)
		}
		f.observe(t, f.terminal(t, sessions.TurnFailed, "engine_failed", code), 1)
	}
	for _, tc := range []struct{ status, core, code string }{{sessions.TurnFailed, "engine_failed", "context_length_exceeded"}, {sessions.TurnFailed, "engine_failed", "cyber_policy"}, {sessions.TurnFailed, "engine_failed", "harness_error"}, {sessions.TurnFailed, "engine_failed", "untrusted raw text"}, {sessions.TurnFailed, "input_not_applied", "authentication_error"}, {sessions.TurnFailed, "device_disconnected", "authentication_error"}, {sessions.TurnCancelled, "engine_failed", "authentication_error"}} {
		f.observe(t, f.terminal(t, tc.status, tc.core, tc.code), 0)
	}
	success := f.terminal(t, sessions.TurnCompleted, "", "")
	f.observe(t, success, 1)
	n, err := f.s.ObserveDeploymentModelProvider(t.Context(), uuid.NewString(), f.session.ID, success.ID)
	if n != 0 || err != nil {
		t.Fatal("foreign tenant observed", err)
	}
	view, _ := f.s.ListDeploymentModelProviders(t.Context())
	if view[0].LastUsedAt == nil || view[0].LastErrorAt == nil {
		t.Fatal("safe observations missing")
	}
	old := f.revision(t)
	reset, err := f.s.SetDeploymentModelProvider(observationAdmin(t), "codex", v1.ModelConfigurationInput{ModelProvider: *f.input.ModelProvider, Model: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, _ := f.s.DeploymentModelProvider(t.Context(), "codex")
	if snapshot.Revision == old || reset.LastUsedAt != nil || reset.LastErrorAt != nil || reset.LastErrorCode != nil {
		t.Fatal("identical PUT did not reset")
	}
	f.observe(t, success, 0)
}
func TestDeploymentObservationSourceAndHistoricalExclusion(t *testing.T) {
	f := newProviderObservationFixture(t)
	for _, source := range []string{"session", "agent", "unknown", "deployment"} {
		input := f.input
		input.IdempotencyKey = uuid.NewString()
		projection := *input.ExecutionConfiguration
		input.ExecutionConfiguration = &projection
		input.ModelProviderSource = source
		// Historical metadata may name deployment but has no frozen private UUID.
		if source == "deployment" {
			input.DeploymentProviderRevision = uuid.Nil
		} else {
			input.Configuration = json.RawMessage(`{"agent":{"model":"frozen-model"},"environment":{"type":"openai_hosted"}}`)
			if source == "unknown" {
				input.ModelProviderSource = "session"
			}
		}
		projection.ModelProvider = v1.ExecutionProviderSelection{Source: source, Status: "available", Configuration: input.ModelProvider.SafeView()}
		session, err := f.s.CreateSession(t.Context(), f.tenant, input)
		if err != nil {
			t.Fatal(source, err)
		}
		var revision pgtype.UUID
		if err = f.pool.QueryRow(t.Context(), "SELECT deployment_provider_revision FROM session_execution_configuration WHERE session_id=$1", session.ID).Scan(&revision); err != nil || revision.Valid {
			t.Fatal("non-deployment/historical revision persisted", source, err)
		}
		// Use a committed fixture outcome directly: hosted dispatch is a separate gate.
		id := uuid.NewString()
		if _, err = f.pool.Exec(t.Context(), "INSERT INTO turns(id,session_id,status,outcome,completed_at) VALUES($1,$2,'completed','{}',clock_timestamp())", id, session.ID); err != nil {
			t.Fatal(err)
		}
		n, err := f.s.ObserveDeploymentModelProvider(t.Context(), f.tenant, session.ID, id)
		if err != nil || n != 0 {
			t.Fatal("ineligible source observed", source, err)
		}
	}
}
func TestDeploymentObservationConcurrentThrottleAndRecovery(t *testing.T) {
	f := newProviderObservationFixture(t)
	successes := []sessions.Turn{}
	failures := []sessions.Turn{}
	for range 8 {
		successes = append(successes, f.terminal(t, sessions.TurnCompleted, "", ""))
	}
	for i := range 8 {
		code := []string{"authentication_error", "rate_limit_exceeded"}[i%2]
		failures = append(failures, f.terminal(t, sessions.TurnFailed, "engine_failed", code))
	}
	concurrent := func(turns []sessions.Turn, want int64) {
		t.Helper()
		var wg sync.WaitGroup
		counts := make(chan int64, len(turns))
		errs := make(chan error, len(turns))
		for _, turn := range turns {
			wg.Add(1)
			go func() {
				defer wg.Done()
				n, err := f.s.ObserveDeploymentModelProvider(t.Context(), f.tenant, f.session.ID, turn.ID)
				counts <- n
				errs <- err
			}()
		}
		wg.Wait()
		close(counts)
		close(errs)
		var n int64
		for v := range counts {
			n += v
		}
		for err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
		if n != want {
			t.Fatalf("concurrent writes=%d want=%d", n, want)
		}
	}
	concurrent(successes, 1)
	concurrent(failures, 1)
	concurrent(successes, 1)
	concurrent(successes, 0)
	concurrent(failures, 0)
}

// Only the DB clock sample is controlled; execute the actual generated query,
// retaining PostgreSQL locking, predicates, constraints and write count.
type observationClockDB struct {
	*pgxpool.Pool
	at    time.Time
	query string
}

func (db *observationClockDB) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	db.query = sql
	sql = strings.ReplaceAll(sql, "clock_timestamp()", "'"+db.at.Format(time.RFC3339Nano)+"'::timestamptz")
	return db.Pool.Exec(ctx, sql, args...)
}
func TestDeploymentObservationClockBoundariesRollbackAndPlan(t *testing.T) {
	f := newProviderObservationFixture(t)
	success := f.terminal(t, sessions.TurnCompleted, "", "")
	failure := f.terminal(t, sessions.TurnFailed, "engine_failed", "authentication_error")
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	db := &observationClockDB{Pool: f.pool}
	q := sqlc.New(db)
	observe := func(turn sessions.Turn, offset time.Duration, want int64) {
		t.Helper()
		db.at = base.Add(offset)
		lookup, _ := turnLookup(f.tenant, f.session.ID, turn.ID)
		n, err := q.ObserveDeploymentModelProvider(t.Context(), sqlc.ObserveDeploymentModelProviderParams{TenantID: lookup.TenantID, SessionID: lookup.SessionID, TurnID: lookup.ID})
		if err != nil || n != want {
			t.Fatalf("at %s got %d want %d: %v", offset, n, want, err)
		}
	}
	observe(success, 0, 1)
	observe(failure, time.Second, 1)
	observe(success, 2*time.Second, 1)
	observe(failure, 30*time.Second, 0)
	observe(failure, 31*time.Second, 1)
	observe(success, 31*time.Second, 1)
	observe(success, 31*time.Second, 0)
	// A new accepted error can recover once even if the clock moves backwards.
	observe(failure, 61*time.Second, 1)
	observe(success, -time.Second, 1)
	observe(success, -time.Second, 0)
	observe(success, 29*time.Second, 1)
	observe(success, 59*time.Second-time.Microsecond, 0)
	observe(success, 59*time.Second, 1)
	var actual time.Time
	if err := f.pool.QueryRow(t.Context(), "SELECT last_used_at FROM deployment_model_providers").Scan(&actual); err != nil || !actual.Equal(base.Add(59*time.Second)) {
		t.Fatal("timestamp was clamped", err)
	}
	lookup, _ := turnLookup(f.tenant, f.session.ID, success.ID)
	// EXPLAIN uses the normal production planner and does not execute the update.
	// Tenant/Session and root Turn lookups must constrain the default lookup.
	tx, err := f.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	rows, err := tx.Query(t.Context(), "EXPLAIN "+db.query, lookup.TenantID, lookup.SessionID, lookup.ID)
	if err != nil {
		t.Fatal(err)
	}
	var plan strings.Builder
	for rows.Next() {
		var line string
		_ = rows.Scan(&line)
		plan.WriteString(line)
	}
	rows.Close()
	if rows.Err() != nil || (!strings.Contains(plan.String(), "deployment_model_providers_pkey") || (!strings.Contains(plan.String(), "turns_pkey") && !strings.Contains(plan.String(), "turns_session_id_id_key"))) {
		t.Fatal("bounded lookup indexes absent", rows.Err(), plan.String())
	}
}
func TestDeploymentObservationReplacementLockRecheckAndTimeout(t *testing.T) {
	for _, remove := range []bool{false, true} {
		t.Run(fmt.Sprint("delete=", remove), func(t *testing.T) {
			f := newProviderObservationFixture(t)
			turn := f.terminal(t, sessions.TurnCompleted, "", "")
			tx, err := f.pool.Begin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(context.Background())
			if _, err = tx.Exec(t.Context(), "SELECT harness FROM deployment_model_providers FOR UPDATE"); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 80*time.Millisecond)
			defer cancel()
			if n, err := f.s.ObserveDeploymentModelProvider(ctx, f.tenant, f.session.ID, turn.ID); err == nil || n != 0 {
				t.Fatal("locked observation did not time out")
			}
			done := make(chan int64, 1)
			errs := make(chan error, 1)
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				n, err := f.s.ObserveDeploymentModelProvider(ctx, f.tenant, f.session.ID, turn.ID)
				done <- n
				errs <- err
			}()
			// Verify it actually reached a PostgreSQL lock wait, rather than racing on
			// goroutine scheduling, before replacing/deleting the selected revision.
			deadline := time.Now().Add(time.Second)
			waiting := false
			for time.Now().Before(deadline) {
				if err = f.pool.QueryRow(t.Context(), "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '%ObserveDeploymentModelProvider%')").Scan(&waiting); err != nil {
					t.Fatal(err)
				}
				if waiting {
					break
				}
				time.Sleep(time.Millisecond)
			}
			if !waiting {
				t.Fatal("observation never waited on row lock")
			}
			if remove {
				_, err = tx.Exec(t.Context(), "DELETE FROM deployment_model_providers")
			} else {
				_, err = tx.Exec(t.Context(), "UPDATE deployment_model_providers SET revision=$1", uuid.New())
			}
			if err != nil {
				t.Fatal(err)
			}
			if err = tx.Commit(t.Context()); err != nil {
				t.Fatal(err)
			}
			if n := <-done; n != 0 {
				t.Fatal("stale revision updated replacement", n)
			}
			if err = <-errs; err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestDeploymentObservationMigrationRoundTrip(t *testing.T) {
	f := newProviderObservationFixture(t)
	old := f.revision(t)
	var secret []byte
	if err := f.pool.QueryRow(t.Context(), "SELECT encrypted_config FROM deployment_model_providers").Scan(&secret); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../../migrations/000084_deployment_provider_observations.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(string(raw), "-- +goose Down")
	if _, err = f.pool.Exec(t.Context(), parts[1]); err != nil {
		t.Fatal(err)
	}
	if _, err = f.pool.Exec(t.Context(), parts[0]); err != nil {
		t.Fatal(err)
	}
	var revision uuid.UUID
	var frozen pgtype.UUID
	var preserved []byte
	if err = f.pool.QueryRow(t.Context(), "SELECT revision,encrypted_config FROM deployment_model_providers").Scan(&revision, &preserved); err != nil {
		t.Fatal(err)
	}
	if err = f.pool.QueryRow(t.Context(), "SELECT deployment_provider_revision FROM session_execution_configuration WHERE session_id=$1", f.session.ID).Scan(&frozen); err != nil {
		t.Fatal(err)
	}
	if revision == old || frozen.Valid || string(secret) != string(preserved) {
		t.Fatal("migration recreated historical identity or changed ciphertext")
	}
	frozenProvider, err := f.s.SessionModelExecution(t.Context(), f.tenant, f.session.ID)
	if err != nil || frozenProvider == nil || *frozenProvider != *f.input.ModelProvider {
		t.Fatal("migration/replacement changed Session bundle", err)
	}
	f.observe(t, f.terminal(t, sessions.TurnCompleted, "", ""), 0)
}
