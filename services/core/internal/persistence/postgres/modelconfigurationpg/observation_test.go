package modelconfigurationpg_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/modelconfiguration"
)

// The statement and ShouldObserveProvider classify the shared cases the same
// way; the domain's test checks the rule against the same file.
func TestObservationEligibilityMatchesTheRuleAndResetsOnReplace(t *testing.T) {
	raw, err := os.ReadFile("../../../modelconfiguration/testdata/observation_cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Status          string `json:"status"`
		ErrorCode       string `json:"error_code"`
		EngineErrorCode string `json:"engine_error_code"`
		Observed        bool   `json:"observed"`
	}
	if err := json.Unmarshal(raw, &cases); err != nil || len(cases) == 0 {
		t.Fatal("observation cases unreadable", err)
	}
	o := newObserved(t)
	for _, tc := range cases {
		o.resetObservations(t)
		want := int64(0)
		if tc.Observed {
			want = 1
		}
		if modelconfiguration.ShouldObserveProvider(tc.Status, tc.ErrorCode, tc.EngineErrorCode) != tc.Observed {
			t.Fatalf("rule disagrees with %+v", tc)
		}
		o.observe(t, o.terminal(t, o.session.ID, tc.Status, tc.ErrorCode, tc.EngineErrorCode), want)
	}
	o.resetObservations(t)
	success := o.terminal(t, o.session.ID, "completed", "", "")
	failure := o.terminal(t, o.session.ID, "failed", "engine_failed", "authentication_error")
	o.observe(t, success, 1)
	o.observe(t, failure, 1)
	n, err := o.adapter.ObserveDeploymentModelProvider(t.Context(), modelconfiguration.Observation{TenantID: uuid.NewString(), SessionID: o.session.ID, TurnID: success})
	if n != 0 || err != nil {
		t.Fatal("foreign tenant observed", err)
	}
	if view := o.list(t); view[0].LastUsedAt == nil || view[0].LastErrorAt == nil || view[0].LastErrorCode == nil || *view[0].LastErrorCode != "authentication_error" {
		t.Fatal("safe observations missing", view)
	}
	old := o.resolve(t).Revision
	reset := o.replace(t, *o.input.ModelProvider)
	if o.resolve(t).Revision == old || reset.LastUsedAt != nil || reset.LastErrorAt != nil || reset.LastErrorCode != nil {
		t.Fatal("identical replacement did not reset")
	}
	o.observe(t, success, 0)
	if _, err := o.adapter.ObserveDeploymentModelProvider(t.Context(), modelconfiguration.Observation{TenantID: o.tenant, SessionID: o.session.ID, TurnID: "not-a-uuid"}); !errors.Is(err, modelconfiguration.ErrInvalidObservation) {
		t.Fatal("invalid identifier", err)
	}
}

func TestObservationExcludesOtherSourcesAndHistoricalSessions(t *testing.T) {
	o := newObserved(t)
	for _, source := range []string{"session", "agent", "unknown", "deployment"} {
		input := o.input
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
		session, err := o.sessions.CreateSession(t.Context(), o.tenant, input)
		if err != nil {
			t.Fatal(source, err)
		}
		var revision pgtype.UUID
		if err = o.pool.QueryRow(t.Context(), "SELECT deployment_provider_revision FROM session_execution_configuration WHERE session_id=$1", session.ID).Scan(&revision); err != nil || revision.Valid {
			t.Fatal("non-deployment or historical revision persisted", source, err)
		}
		turn := o.terminal(t, session.ID, "completed", "", "")
		n, err := o.adapter.ObserveDeploymentModelProvider(t.Context(), modelconfiguration.Observation{TenantID: o.tenant, SessionID: session.ID, TurnID: turn})
		if err != nil || n != 0 {
			t.Fatal("ineligible source observed", source, err)
		}
	}
}

func TestObservationIgnoresActiveAndNonRootTurns(t *testing.T) {
	o := newObserved(t)
	id := uuid.NewString()
	if _, err := o.pool.Exec(t.Context(), "INSERT INTO turns(id,session_id,status) VALUES($1,$2,'queued')", id, o.session.ID); err != nil {
		t.Fatal(err)
	}
	o.observe(t, id, 0)
	if _, err := o.pool.Exec(t.Context(), "UPDATE turns SET status='waiting', started_at=clock_timestamp() WHERE id=$1", id); err != nil {
		t.Fatal(err)
	}
	o.observe(t, id, 0)
	// Child Turn identifiers live outside turns. An absent root identifier is
	// rejected by the same ownership join, without a child-history lookup.
	o.observe(t, "ffffffff-ffff-4fff-bfff-ffffffffffff", 0)
}

func TestObservationConcurrentThrottleAndRecovery(t *testing.T) {
	o := newObserved(t)
	var successes, failures []string
	for range 8 {
		successes = append(successes, o.terminal(t, o.session.ID, "completed", "", ""))
	}
	for i := range 8 {
		failures = append(failures, o.terminal(t, o.session.ID, "failed", "engine_failed", []string{"authentication_error", "rate_limit_exceeded"}[i%2]))
	}
	concurrent := func(turns []string, want int64) {
		t.Helper()
		var wg sync.WaitGroup
		counts := make(chan int64, len(turns))
		errs := make(chan error, len(turns))
		for _, turn := range turns {
			wg.Go(func() {
				n, err := o.adapter.ObserveDeploymentModelProvider(t.Context(), modelconfiguration.Observation{TenantID: o.tenant, SessionID: o.session.ID, TurnID: turn})
				counts <- n
				errs <- err
			})
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

// Only the database clock sample is controlled; the generated statement runs
// with its locking, predicates, constraints and write count.
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

func observationParams(t *testing.T, o observed, turn string) sqlc.ObserveDeploymentModelProviderParams {
	t.Helper()
	id := func(value string) pgtype.UUID { return pgtype.UUID{Bytes: uuid.MustParse(value), Valid: true} }
	return sqlc.ObserveDeploymentModelProviderParams{TenantID: id(o.tenant), SessionID: id(o.session.ID), TurnID: id(turn)}
}

func TestObservationClockBoundariesAndPlan(t *testing.T) {
	o := newObserved(t)
	success := o.terminal(t, o.session.ID, "completed", "", "")
	failure := o.terminal(t, o.session.ID, "failed", "engine_failed", "authentication_error")
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	db := &observationClockDB{Pool: o.pool}
	q := sqlc.New(db)
	observe := func(turn string, offset time.Duration, want int64) {
		t.Helper()
		db.at = base.Add(offset)
		n, err := q.ObserveDeploymentModelProvider(t.Context(), observationParams(t, o, turn))
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
	if err := o.pool.QueryRow(t.Context(), "SELECT last_used_at FROM deployment_model_providers").Scan(&actual); err != nil || !actual.Equal(base.Add(59*time.Second)) {
		t.Fatal("timestamp was clamped", err)
	}
	params := observationParams(t, o, success)
	// EXPLAIN uses the normal planner and does not execute the update. The
	// tenant, Session and root Turn lookups must constrain the default lookup.
	tx, err := o.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	rows, err := tx.Query(t.Context(), "EXPLAIN "+db.query, params.TenantID, params.SessionID, params.TurnID)
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
	if rows.Err() != nil || !strings.Contains(plan.String(), "deployment_model_providers_pkey") || (!strings.Contains(plan.String(), "turns_pkey") && !strings.Contains(plan.String(), "turns_session_id_id_key")) {
		t.Fatal("bounded lookup indexes absent", rows.Err(), plan.String())
	}
}

func TestObservationRechecksTheRevisionAfterItsLockAndTimesOut(t *testing.T) {
	for _, remove := range []bool{false, true} {
		t.Run(fmt.Sprint("delete=", remove), func(t *testing.T) {
			o := newObserved(t)
			turn := o.terminal(t, o.session.ID, "completed", "", "")
			tx, err := o.pool.Begin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(context.Background())
			if _, err = tx.Exec(t.Context(), "SELECT harness FROM deployment_model_providers FOR UPDATE"); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 80*time.Millisecond)
			defer cancel()
			if n, err := o.adapter.ObserveDeploymentModelProvider(ctx, modelconfiguration.Observation{TenantID: o.tenant, SessionID: o.session.ID, TurnID: turn}); err == nil || n != 0 {
				t.Fatal("locked observation did not time out")
			}
			type result struct {
				n   int64
				err error
			}
			done := make(chan result, 1)
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				n, err := o.adapter.ObserveDeploymentModelProvider(ctx, modelconfiguration.Observation{TenantID: o.tenant, SessionID: o.session.ID, TurnID: turn})
				done <- result{n, err}
			}()
			// Replace or delete the selected revision only once the observation
			// waits on its row lock.
			waitForLock(t, o.pool, "ObserveDeploymentModelProvider")
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
			if got := <-done; got.n != 0 || got.err != nil {
				t.Fatal("stale revision updated its replacement", got.n, got.err)
			}
		})
	}
}

func TestObservationSamplesItsClockAfterTheRowLock(t *testing.T) {
	o := newObserved(t)
	turn := o.terminal(t, o.session.ID, "completed", "", "")
	tx, err := o.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(t.Context(), "SELECT harness FROM deployment_model_providers FOR UPDATE"); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		_, err := o.adapter.ObserveDeploymentModelProvider(t.Context(), modelconfiguration.Observation{TenantID: o.tenant, SessionID: o.session.ID, TurnID: turn})
		result <- err
	}()
	waitForLock(t, o.pool, "ObserveDeploymentModelProvider")
	var beforeUnlock time.Time
	if err = tx.QueryRow(t.Context(), "SELECT clock_timestamp()").Scan(&beforeUnlock); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err = <-result; err != nil {
		t.Fatal(err)
	}
	if rows := o.list(t); rows[0].LastUsedAt == nil || rows[0].LastUsedAt.Before(beforeUnlock) {
		t.Fatal("receipt clock sampled before the row lock")
	}
}

func TestReplacementWaitsForAWinningObservationAndClearsIt(t *testing.T) {
	o := newObserved(t)
	turn := o.terminal(t, o.session.ID, "completed", "", "")
	tx, err := o.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	n, err := sqlc.New(tx).ObserveDeploymentModelProvider(t.Context(), observationParams(t, o, turn))
	if err != nil || n != 1 {
		t.Fatal("observation did not win the row lock", n, err)
	}
	result := make(chan error, 1)
	go func() {
		_, err := o.service.Replace(admin(t), modelconfiguration.Replacement{Harness: "codex", Configuration: v1.ModelConfigurationInput{ModelProvider: *o.input.ModelProvider, Model: "fixture"}})
		result <- err
	}()
	waitForLock(t, o.pool, "UpsertDeploymentModelProvider")
	if err = tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err = <-result; err != nil {
		t.Fatal(err)
	}
	if rows := o.list(t); rows[0].LastUsedAt != nil || rows[0].LastErrorCode != nil || rows[0].LastErrorAt != nil {
		t.Fatal("replacement kept the old revision's observation")
	}
	o.observe(t, turn, 0)
}

func TestObservationMigrationRoundTrip(t *testing.T) {
	o := newObserved(t)
	old := o.resolve(t).Revision
	var secret, sessionBundle []byte
	if err := o.pool.QueryRow(t.Context(), "SELECT (SELECT encrypted_config FROM deployment_model_providers), (SELECT encrypted_config FROM session_model_execution WHERE session_id=$1)", o.session.ID).Scan(&secret, &sessionBundle); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../../../../migrations/000084_deployment_provider_observations.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(string(raw), "-- +goose Down")
	if _, err = o.pool.Exec(t.Context(), parts[1]); err != nil {
		t.Fatal(err)
	}
	if _, err = o.pool.Exec(t.Context(), parts[0]); err != nil {
		t.Fatal(err)
	}
	var revision uuid.UUID
	var frozen pgtype.UUID
	var preserved, preservedSession []byte
	if err = o.pool.QueryRow(t.Context(), "SELECT revision, encrypted_config FROM deployment_model_providers").Scan(&revision, &preserved); err != nil {
		t.Fatal(err)
	}
	if err = o.pool.QueryRow(t.Context(), "SELECT c.deployment_provider_revision, e.encrypted_config FROM session_execution_configuration c JOIN session_model_execution e USING (session_id) WHERE c.session_id=$1", o.session.ID).Scan(&frozen, &preservedSession); err != nil {
		t.Fatal(err)
	}
	if revision == old || frozen.Valid || string(secret) != string(preserved) {
		t.Fatal("migration recreated historical identity or changed ciphertext")
	}
	if string(sessionBundle) != string(preservedSession) {
		t.Fatal("migration changed the Session bundle")
	}
	o.observe(t, o.terminal(t, o.session.ID, "completed", "", ""), 0)
}
