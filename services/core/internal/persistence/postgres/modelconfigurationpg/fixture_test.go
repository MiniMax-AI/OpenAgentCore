package modelconfigurationpg_test

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/adminaudit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment/placement"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/identity"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/modelconfiguration"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/modelconfigurationpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgtest"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/providers"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
)

// Deployment defaults are deployment-wide, so every test opens its own
// database.
type fixture struct {
	pool    *pgxpool.Pool
	cipher  *credentialcrypto.Cipher
	adapter *modelconfigurationpg.Store
	service *modelconfiguration.Service
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	pool := pgtest.OpenIsolated(t, nil)
	cipher, err := credentialcrypto.New(bytes.Repeat([]byte{47}, 32))
	if err != nil {
		t.Fatal(err)
	}
	adapter := modelconfigurationpg.New(pgunit.NewPool(pool), cipher)
	service, err := modelconfiguration.NewService(adapter)
	if err != nil {
		t.Fatal(err)
	}
	return fixture{pool, cipher, adapter, service}
}

func admin(t *testing.T) context.Context {
	return adminaudit.WithSource(t.Context(), adminaudit.Source{CredentialID: "fixture-admin", RequestID: uuid.NewString(), TraceID: uuid.NewString()})
}

var fixtureProvider = v1.ModelProviderInput{Protocol: "responses", BaseURL: "https://model.fixture.example/v1", APIKey: "fixture-model-key"}

func (f fixture) replace(t *testing.T, provider v1.ModelProviderInput) modelconfiguration.Configuration {
	t.Helper()
	configuration, err := f.service.Replace(admin(t), modelconfiguration.Replacement{Harness: "codex", Configuration: v1.ModelConfigurationInput{ModelProvider: provider, Model: "fixture"}})
	if err != nil {
		t.Fatal(err)
	}
	return configuration
}

func (f fixture) resolve(t *testing.T) *modelconfiguration.Snapshot {
	t.Helper()
	snapshot, err := f.service.Resolve(t.Context(), "codex")
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func (f fixture) list(t *testing.T) []modelconfiguration.Configuration {
	t.Helper()
	configurations, err := f.adapter.List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return configurations
}

// observed is a codex default and one Session that froze it. Store builds the
// Session; Turns are committed rows written directly.
type observed struct {
	fixture
	sessions *store.Store
	tenant   string
	input    sessions.CreateSession
	session  sessions.Session
}

func newObserved(t *testing.T) observed {
	t.Helper()
	f := newFixture(t)
	f.replace(t, fixtureProvider)
	snapshot := f.resolve(t)
	model, harness := "frozen-model", "codex"
	input := sessions.CreateSession{
		Creator: identity.Subject{Kind: "service_account", ID: "fixture"}, Engine: harness, IdempotencyKey: uuid.NewString(),
		Configuration: json.RawMessage(`{"agent":{"model":"frozen-model"},"environment":{"type":"none"}}`),
		ModelProvider: snapshot.Provider, ModelProviderSource: "deployment", DeploymentProviderRevision: snapshot.Revision,
		ExecutionConfiguration: &v1.SessionExecutionConfiguration{
			Model: v1.ExecutionSelection{Value: &model, Source: "deployment"}, Harness: v1.ExecutionSelection{Value: &harness, Source: "deployment"},
			ModelProvider: v1.ExecutionProviderSelection{Source: "deployment"},
		},
	}
	sessions := store.NewWithCredentialCipher(f.pool, f.cipher)
	// cmd/server gives the store its placement rules; hosted creation needs them.
	rules, err := placement.NewRules(providers.Builtin(), "")
	if err != nil {
		t.Fatal(err)
	}
	sessions.SetPlacement(rules)
	tenant := uuid.NewString()
	session, err := sessions.CreateSession(t.Context(), tenant, input)
	if err != nil {
		t.Fatal(err)
	}
	return observed{f, sessions, tenant, input, session}
}

// terminal commits a root Turn with the outcome fields the observation reads.
func (o observed) terminal(t *testing.T, sessionID, status, errorCode, engineErrorCode string) string {
	t.Helper()
	outcome, _ := json.Marshal(map[string]string{"error_code": errorCode, "engine_error_code": engineErrorCode})
	id := uuid.NewString()
	if _, err := o.pool.Exec(t.Context(), "INSERT INTO turns(id,session_id,status,outcome,completed_at) VALUES($1,$2,$3,$4,clock_timestamp())", id, sessionID, status, outcome); err != nil {
		t.Fatal(err)
	}
	return id
}

func (o observed) observe(t *testing.T, turnID string, want int64) {
	t.Helper()
	n, err := o.adapter.ObserveDeploymentModelProvider(t.Context(), modelconfiguration.Observation{TenantID: o.tenant, SessionID: o.session.ID, TurnID: turnID})
	if err != nil || n != want {
		t.Fatalf("observation writes=%d want=%d err=%v", n, want, err)
	}
}

func (o observed) resetObservations(t *testing.T) {
	t.Helper()
	if _, err := o.pool.Exec(t.Context(), "UPDATE deployment_model_providers SET last_used_at=NULL,last_error_at=NULL,last_error_code=NULL,recovery_pending=false"); err != nil {
		t.Fatal(err)
	}
}

// waitForLock waits until a statement matching query waits on a row lock.
func waitForLock(t *testing.T, pool *pgxpool.Pool, query string) {
	t.Helper()
	for range 1000 {
		var waiting bool
		if err := pool.QueryRow(t.Context(), "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE $1)", "%"+query+"%").Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal(query, "never waited on a row lock")
}
