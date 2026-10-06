package store

import (
	"bytes"
	"encoding/json"
	"testing"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/adminaudit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/modelconfiguration"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/modelconfigurationpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/google/uuid"
)

// A Session keeps the deployment default and revision it resolved. Replacing,
// removing or recreating the default never changes a created Session, a retry
// that resolved a newer default replays the original, and a Session that froze
// an older revision never updates the current default's observations.
func TestSessionCreationKeepsItsResolvedDeploymentRevision(t *testing.T) {
	s, pool := newManagedTestStore(t)
	cipher, err := credentialcrypto.New(bytes.Repeat([]byte{73}, 32))
	if err != nil {
		t.Fatal(err)
	}
	defaults := modelconfigurationpg.New(pgunit.NewPool(pool), cipher)
	service, err := modelconfiguration.NewService(defaults)
	if err != nil {
		t.Fatal(err)
	}
	admin := adminaudit.WithSource(t.Context(), adminaudit.Source{CredentialID: "fixture-admin", RequestID: uuid.NewString(), TraceID: uuid.NewString()})
	replace := func(provider v1.ModelProviderInput) {
		t.Helper()
		if _, err := service.Replace(admin, modelconfiguration.Replacement{Harness: "codex", Configuration: v1.ModelConfigurationInput{ModelProvider: provider, Model: "fixture"}}); err != nil {
			t.Fatal(err)
		}
	}
	resolve := func() *modelconfiguration.Snapshot {
		t.Helper()
		snapshot, err := service.Resolve(t.Context(), "codex")
		if err != nil || snapshot == nil {
			t.Fatal("deployment default did not resolve", err)
		}
		return snapshot
	}
	revision := func(session string) uuid.UUID {
		t.Helper()
		var value uuid.UUID
		if err := pool.QueryRow(t.Context(), "SELECT deployment_provider_revision FROM session_execution_configuration WHERE session_id=$1", session).Scan(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	tenant := uuid.NewString()
	observe := func(session string, status, coreCode, nativeCode string) {
		t.Helper()
		receipt := submitMessage(t, s, tenant, session, uuid.NewString())
		transition(t, s, tenant, session, receipt.TurnID, sessions.TurnQueued, sessions.TurnInProgress)
		outcome, _ := json.Marshal(map[string]string{"error_code": coreCode, "engine_error_code": nativeCode})
		turn, err := completeExecution(t.Context(), t, s, tenant, session, receipt.TurnID, status, outcome, "", receipt.Sequence)
		if err != nil {
			t.Fatal(err)
		}
		if n, err := defaults.ObserveDeploymentModelProvider(t.Context(), modelconfiguration.Observation{TenantID: tenant, SessionID: session, TurnID: turn.ID}); err != nil || n != 0 {
			t.Fatalf("observation writes=%d want=0 err=%v", n, err)
		}
	}
	replace(*FixtureModelProvider("codex"))
	before := resolve()
	input := executionProjectionInput("deployment")
	input.Configuration = json.RawMessage(`{"agent":{"model":"frozen-model"},"environment":{"type":"none"}}`)
	input.ModelProvider, input.ModelProviderSource, input.DeploymentProviderRevision = before.Provider, "deployment", before.Revision
	input.ExecutionConfiguration.ModelProvider = v1.ExecutionProviderSelection{Source: "deployment"}
	session, err := s.CreateSession(t.Context(), tenant, input)
	if err != nil {
		t.Fatal(err)
	}
	original := revision(session.ID)
	// Resolution and insertion have independent boundaries: retain the tuple while
	// a replacement lands, then create with that exact earlier tuple.
	replacement := *before.Provider
	replacement.APIKey = "different-fixture-key"
	replace(replacement)
	if _, err = pool.Exec(t.Context(), "UPDATE deployment_model_providers SET updated_at='2000-01-01'"); err != nil {
		t.Fatal(err)
	}
	staleInput := input
	staleInput.IdempotencyKey = uuid.NewString()
	stale, err := s.CreateSession(t.Context(), tenant, staleInput)
	if err != nil {
		t.Fatal(err)
	}
	if revision(stale.ID) != original {
		t.Fatal("tuple revision changed during insertion")
	}
	frozen, err := sessionAdapter(s).SessionModelExecution(t.Context(), tenant, stale.ID)
	if err != nil || frozen == nil || *frozen != *before.Provider {
		t.Fatal("frozen tuple bundle changed", err)
	}
	observe(stale.ID, sessions.TurnFailed, "engine_failed", "authentication_error")
	current := resolve()
	retry := input
	retry.ModelProvider = current.Provider
	retry.DeploymentProviderRevision = current.Revision
	replay, err := s.CreateSession(t.Context(), tenant, retry)
	if err != nil || replay.ID != session.ID || revision(session.ID) != original {
		t.Fatal("retry replaced frozen metadata", err)
	}
	if err = service.Delete(admin, "codex"); err != nil {
		t.Fatal(err)
	}
	replace(*before.Provider)
	if recreated := resolve(); recreated.Revision == original || recreated.Revision == current.Revision {
		t.Fatal("revision reused")
	}
	frozenProvider, err := sessionAdapter(s).SessionModelExecution(t.Context(), tenant, session.ID)
	if err != nil || frozenProvider == nil || *frozenProvider != *input.ModelProvider {
		t.Fatal("replacement changed the Session bundle", err)
	}
	observe(session.ID, sessions.TurnCompleted, "", "")
}
