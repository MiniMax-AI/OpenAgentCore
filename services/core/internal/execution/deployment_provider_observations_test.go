package execution

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/adminaudit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/identity"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/modelconfiguration"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/modelconfigurationpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type finishObservationFixture struct {
	s          *store.Store
	writer     *store.Store
	lease      Ownership
	pool       *pgxpool.Pool
	defaults   *modelconfigurationpg.Store
	tenant     string
	session    sessions.Session
	dispatcher Dispatcher
}

func newFinishObservationFixture(t *testing.T, maxConnections int32) finishObservationFixture {
	t.Helper()
	var cfg *pgxpool.Config
	s, owner, _, _ := resetManagerStoreConfig(t, func(c *pgxpool.Config) {
		if maxConnections > 0 {
			c.MaxConns = maxConnections
		}
		cfg = c.Copy()
	})
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	cipher, err := credentialcrypto.New(bytes.Repeat([]byte{8}, 32))
	if err != nil {
		t.Fatal(err)
	}
	defaults := modelconfigurationpg.New(pgunit.NewPool(pool), cipher)
	service, err := modelconfiguration.NewService(defaults)
	if err != nil {
		t.Fatal(err)
	}
	admin := adminaudit.WithSource(t.Context(), adminaudit.Source{CredentialID: "fixture-admin", RequestID: uuid.NewString(), TraceID: uuid.NewString()})
	provider := v1.ModelProviderInput{Protocol: "responses", BaseURL: "https://fixture.example/v1", APIKey: "fixture-only"}
	if _, err = service.Replace(admin, modelconfiguration.Replacement{Harness: "codex", Configuration: v1.ModelConfigurationInput{ModelProvider: provider, Model: "fixture"}}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := service.Resolve(t.Context(), "codex")
	if err != nil {
		t.Fatal(err)
	}
	tenant := uuid.NewString()
	model, harness := "fixture-model", "codex"
	input := sessions.CreateSession{Creator: identity.Subject{Kind: "service_account", ID: "fixture"}, Engine: harness, IdempotencyKey: uuid.NewString(), Configuration: json.RawMessage(`{"agent":{"model":"fixture-model"},"environment":{"type":"none"}}`), ModelProvider: snapshot.Provider, ModelProviderSource: "deployment", DeploymentProviderRevision: snapshot.Revision,
		ExecutionConfiguration: &v1.SessionExecutionConfiguration{Model: v1.ExecutionSelection{Value: &model, Source: "session"}, Harness: v1.ExecutionSelection{Value: &harness, Source: "deployment"}, ModelProvider: v1.ExecutionProviderSelection{Source: "deployment"}}}
	session, err := s.CreateSession(t.Context(), tenant, input)
	if err != nil {
		t.Fatal(err)
	}
	dispatcher, err := (&Dispatcher{Observer: defaults}).Bind(owner)
	if err != nil {
		t.Fatal(err)
	}
	return finishObservationFixture{s, owner.Store, owner.Lease, pool, defaults, tenant, session, *dispatcher}
}
func (f finishObservationFixture) start(t *testing.T) sessions.InputReceipt {
	t.Helper()
	receipt, err := f.s.SubmitMessage(t.Context(), f.tenant, f.session.ID, uuid.NewString(), json.RawMessage(`{"input":[{"role":"user","content":[{"type":"input_text","text":"fixture"}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.writer.TransitionTurn(t.Context(), f.tenant, f.session.ID, receipt.TurnID, sessions.TurnTransition{ExpectedStatus: sessions.TurnQueued, Status: sessions.TurnInProgress}); err != nil {
		t.Fatal(err)
	}
	return receipt
}
func (f finishObservationFixture) fields(t *testing.T) (*time.Time, *string) {
	t.Helper()
	rows, err := f.defaults.List(t.Context())
	if err != nil || len(rows) != 1 {
		t.Fatal(err)
	}
	return rows[0].LastUsedAt, rows[0].LastErrorCode
}
func TestFinishRunObservesOnlyFinalCommittedOutcome(t *testing.T) {
	cases := []struct {
		name, status, core, native       string
		unapplied, invalid, used, failed bool
	}{
		{name: "success", status: sessions.TurnCompleted, used: true},
		{name: "provider_failure", status: sessions.TurnFailed, core: "engine_failed", native: "authentication_error", failed: true},
		{name: "runtime_dominates", status: sessions.TurnFailed, core: "device_disconnected", native: "authentication_error"},
		{name: "input_policy", status: sessions.TurnFailed, core: "engine_failed", native: "cyber_policy"},
		{name: "cancelled", status: sessions.TurnCancelled, core: "engine_failed", native: "authentication_error"},
		{name: "fallback", status: sessions.TurnCompleted, core: "engine_failed", native: "authentication_error", unapplied: true},
		{name: "invalid_result", status: sessions.TurnCompleted, invalid: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFinishObservationFixture(t, 0)
			receipt := f.start(t)
			result := Result{ErrorCode: tc.core, EngineErrorCode: tc.native, AppliedThrough: receipt.Sequence}
			if tc.unapplied {
				result.AppliedThrough = 0
			}
			if tc.invalid {
				result.Error = strings.Repeat("x", 513*1024)
			}
			turn, err := f.dispatcher.finishRun(f.tenant, f.session.ID, receipt.TurnID, "fixture-model", result, tc.status)
			if err != nil {
				t.Fatal(err)
			}
			if tc.unapplied || tc.invalid {
				if turn.Status != sessions.TurnFailed {
					t.Fatal("fallback not final", turn.Status)
				}
			}
			used, code := f.fields(t)
			if (used != nil) != tc.used || (code != nil) != tc.failed {
				t.Fatalf("unexpected observation: used=%v code=%v", used, code)
			}
			if _, err = f.pool.Exec(t.Context(), "UPDATE deployment_model_providers SET last_used_at=NULL,last_error_at=NULL,last_error_code=NULL,recovery_pending=false"); err != nil {
				t.Fatal(err)
			}
			_, err = f.dispatcher.finishRun(f.tenant, f.session.ID, receipt.TurnID, "fixture-model", result, tc.status)
			if !errors.Is(err, sessions.ErrTurnConflict) {
				t.Fatal("expected completion conflict", err)
			}
			used, code = f.fields(t)
			if used != nil || code != nil {
				t.Fatal("failed completion observed")
			}
		})
	}
}
func TestFinishRunObservationLockTimeoutAndFailureKeepLease(t *testing.T) {
	for _, mode := range []string{"lock_timeout", "query_failure", "pool_timeout"} {
		t.Run(mode, func(t *testing.T) {
			// One leased connection plus one ordinary connection gives an independently
			// observable pool-acquisition deadline without starving the completion writer.
			max := int32(0)
			if mode == "pool_timeout" {
				max = 1
			}
			f := newFinishObservationFixture(t, 0)
			receipt := f.start(t)
			var cleanup func()
			if mode == "lock_timeout" {
				tx, err := f.pool.Begin(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				if _, err = tx.Exec(t.Context(), "SELECT harness FROM deployment_model_providers FOR UPDATE"); err != nil {
					t.Fatal(err)
				}
				cleanup = func() { _ = tx.Rollback(context.Background()) }
			}
			if mode == "query_failure" {
				_, err := f.pool.Exec(t.Context(), `CREATE FUNCTION reject_provider_observation() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'fixture secret must never be logged'; END $$; CREATE TRIGGER reject_provider_observation BEFORE UPDATE ON deployment_model_providers FOR EACH ROW EXECUTE FUNCTION reject_provider_observation()`)
				if err != nil {
					t.Fatal(err)
				}
			}
			if mode == "pool_timeout" {
				// A separate single-connection Store owns its leased connection. Only the
				// best-effort metadata query needs this exhausted pool after completion.
				cfg := f.pool.Config().Copy()
				cfg.MaxConns = max
				pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
				if err != nil {
					t.Fatal(err)
				}
				defer pool.Close()
				// Observe directly through a Dispatcher using the saturated pool; the final
				// commit below remains on the real existing lease.
				conn, err := pool.Acquire(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				defer conn.Release()
				turn, err := f.writer.CompleteExecution(t.Context(), f.tenant, f.session.ID, receipt.TurnID, sessions.TurnCompleted, json.RawMessage(`{}`), "", receipt.Sequence)
				if err != nil {
					t.Fatal(err)
				}
				d := Dispatcher{Observer: modelconfigurationpg.New(pgunit.NewPool(pool), nil)}
				started := time.Now()
				d.observeDeploymentProvider(f.tenant, f.session.ID, turn)
				if elapsed := time.Since(started); elapsed < 900*time.Millisecond || elapsed > 2*time.Second {
					t.Fatal("pool timeout exceeded observation budget", elapsed)
				}
				if err = f.lease.CheckOwnership(t.Context()); err != nil {
					t.Fatal("observation cancelled lease", err)
				}
				return
			}
			if cleanup != nil {
				defer cleanup()
			}
			started := time.Now()
			turn, err := f.dispatcher.finishRun(f.tenant, f.session.ID, receipt.TurnID, "fixture-model", Result{AppliedThrough: receipt.Sequence}, sessions.TurnCompleted)
			elapsed := time.Since(started)
			if err != nil || turn.Status != sessions.TurnCompleted {
				t.Fatal("observation changed commit result", err, turn.Status)
			}
			if elapsed > 2*time.Second || (mode == "lock_timeout" && elapsed < 900*time.Millisecond) {
				t.Fatal("observation duration", elapsed)
			}
			if cleanup != nil {
				cleanup()
			}
			persisted, err := f.s.GetTurn(t.Context(), f.tenant, f.session.ID, receipt.TurnID)
			if err != nil || persisted.Status != sessions.TurnCompleted {
				t.Fatal("terminal outcome lost", err)
			}
			used, code := f.fields(t)
			if used != nil || code != nil {
				t.Fatal("failed observation wrote")
			}
			if err = f.lease.CheckOwnership(t.Context()); err != nil {
				t.Fatal("observation cancelled execution lease", err)
			}
		})
	}
}
