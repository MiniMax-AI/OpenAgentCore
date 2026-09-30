package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync"
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

func TestSessionCreationIdentityConvergesOnFrozenSnapshot(t *testing.T) {
	s, pool := testStore(t)
	ctx := context.Background()
	tenant := uuid.NewString()
	request := json.RawMessage(`{"agent_id":"source","agent":{"tools":[{"parameters":{"const":9007199254740993}}]}}`)
	const count = 8
	results := make(chan sessions.Creation, count)
	errs := make(chan error, count)
	var wg sync.WaitGroup
	for i := range count {
		wg.Add(1)
		go func() {
			defer wg.Done()
			input := sessions.CreateSession{Creator: FixtureCreator(), Engine: "codex", IdempotencyKey: "same", CreationRequest: request, Configuration: json.RawMessage(fmt.Sprintf(`{"resolved":%d}`, i)), InitialInputs: []sessions.Input{messageInput("one")}}
			result, err := s.CreateSessionStream(ctx, tenant, input)
			results <- result
			errs <- err
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var first sessions.Creation
	created := 0
	for result := range results {
		if first.Session.ID == "" {
			first = result
		}
		if result.Session.ID != first.Session.ID || string(result.Session.Configuration) != string(first.Session.Configuration) {
			t.Fatal("concurrent resolution changed winner", first, result)
		}
		if result.Created {
			created++
			if result.Cursor != 0 {
				t.Fatal("creation cursor passed initial input")
			}
		}
	}
	var turns, inputs int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM turns WHERE session_id=$1`, first.Session.ID).Scan(&turns); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM turn_inputs WHERE session_id=$1`, first.Session.ID).Scan(&inputs); err != nil {
		t.Fatal(err)
	}
	if created != 1 || turns != 1 || inputs != 1 {
		t.Fatal(created, turns, inputs)
	}
	restarted := New(pool)
	retry, err := restarted.FindSessionCreation(ctx, tenant, "same", json.RawMessage(`{"agent":{"tools":[{"parameters":{"const":9007199254740993}}]},"agent_id":"source"}`), FixtureCreator())
	if err != nil || retry.Created || retry.Session.ID != first.Session.ID || retry.Cursor == 0 {
		t.Fatal(retry, err)
	}
	if _, err := restarted.FindSessionCreation(ctx, tenant, "same", json.RawMessage(`{"agent_id":"source","agent":{"tools":[{"parameters":{"const":9007199254740992}}]}}`), FixtureCreator()); !errors.Is(err, sessions.ErrIdempotencyConflict) {
		t.Fatal("numeric identity collapsed", err)
	}
	if _, err := restarted.FindSessionCreation(ctx, uuid.NewString(), "same", request, FixtureCreator()); !errors.Is(err, sessions.ErrNotFound) {
		t.Fatal("foreign lookup", err)
	}
	if _, err := restarted.CreateSession(ctx, tenant, sessions.CreateSession{Creator: FixtureCreator(), Engine: "codex", IdempotencyKey: "same", CreationRequest: json.RawMessage(`{"agent_id":"changed"}`), Configuration: first.Session.Configuration}); !errors.Is(err, sessions.ErrIdempotencyConflict) {
		t.Fatal("changed caller admitted", err)
	}
}

func TestSessionCreationIdentityDoesNotInventHistoricalIntent(t *testing.T) {
	s, pool := testStore(t)
	ctx := context.Background()
	tenant := uuid.NewString()
	input := sessions.CreateSession{Creator: FixtureCreator(), Engine: "codex", IdempotencyKey: "historical", Configuration: json.RawMessage(`{"agent":{"id":"source","instructions":"original"}}`)}
	first, err := s.CreateSession(ctx, tenant, input)
	if err != nil {
		t.Fatal(err)
	}
	input.CreationRequest = json.RawMessage(`{"agent_id":"source"}`)
	if _, err := s.FindSessionCreation(ctx, tenant, input.IdempotencyKey, input.CreationRequest, FixtureCreator()); !errors.Is(err, sessions.ErrNotFound) {
		t.Fatal(err)
	}
	retry, err := s.CreateSession(ctx, tenant, input)
	if err != nil || !reflect.DeepEqual(first, retry) {
		t.Fatal(retry, err)
	}
	var hash *string
	if err := pool.QueryRow(ctx, `SELECT creation_request_hash FROM sessions WHERE id=$1`, first.ID).Scan(&hash); err != nil || hash != nil {
		t.Fatal("historical intent was manufactured", hash, err)
	}
	input.Configuration = json.RawMessage(`{"agent":{"id":"source","instructions":"changed"}}`)
	if _, err := s.CreateSession(ctx, tenant, input); !errors.Is(err, sessions.ErrIdempotencyConflict) {
		t.Fatal("historical retry rules changed", err)
	}
}

// A provider key enters the retry hashes only through a fingerprint keyed by the
// credential key: the same request under two credential keys hashes differently,
// and an intent whose provider cannot be read is rejected, not hashed raw.
func TestProviderKeyEntersRetryHashesOnlyAsKeyedFingerprint(t *testing.T) {
	_, pool := testStore(t)
	provider := &v1.ModelProviderInput{Protocol: "responses", BaseURL: "https://example.com/v1", APIKey: "hash-key-canary"}
	intent, err := json.Marshal(map[string]any{"agent": map[string]string{"model": "m"}, "environment": map[string]string{"type": "openai_hosted"}, "x_agents_core": map[string]any{"model_provider": provider}})
	if err != nil {
		t.Fatal(err)
	}
	input := sessions.CreateSession{Creator: FixtureCreator(), Engine: "codex", IdempotencyKey: "same-request", ModelProvider: provider, ModelProviderSource: v1.ModelProviderSourceSession,
		Configuration: []byte(`{"agent":{"model":"m"},"environment":{"type":"openai_hosted"}}`), CreationRequest: intent}
	var hashes [2][2]string
	for index, seed := range []byte{71, 72} {
		cipher, err := credentialcrypto.New(bytes.Repeat([]byte{seed}, 32))
		if err != nil {
			t.Fatal(err)
		}
		session, err := withPlacement(t, NewWithCredentialCipher(pool, cipher)).CreateSession(t.Context(), uuid.NewString(), input)
		if err != nil {
			t.Fatal(err)
		}
		if err := pool.QueryRow(t.Context(), "SELECT request_hash, creation_request_hash FROM sessions WHERE id=$1", session.ID).Scan(&hashes[index][0], &hashes[index][1]); err != nil {
			t.Fatal(err)
		}
	}
	if hashes[0][0] == hashes[1][0] || hashes[0][1] == hashes[1][1] {
		t.Fatal("retry hashes do not depend on the credential key", hashes)
	}
	cipher, _ := credentialcrypto.New(bytes.Repeat([]byte{71}, 32))
	unreadable := input
	unreadable.IdempotencyKey, unreadable.CreationRequest = "unreadable", json.RawMessage(`{"x_agents_core":{"model_provider":"hash-key-canary"}}`)
	if _, err := withPlacement(t, NewWithCredentialCipher(pool, cipher)).CreateSession(t.Context(), uuid.NewString(), unreadable); !errors.Is(err, sessions.ErrInvalidInput) {
		t.Fatal("unreadable provider intent was hashed", err)
	}
}

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
		turn, err := s.CompleteExecution(t.Context(), tenant, session, receipt.TurnID, status, outcome, "", receipt.Sequence)
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
	frozen, err := s.SessionModelExecution(t.Context(), tenant, stale.ID)
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
	frozenProvider, err := s.SessionModelExecution(t.Context(), tenant, session.ID)
	if err != nil || frozenProvider == nil || *frozenProvider != *input.ModelProvider {
		t.Fatal("replacement changed the Session bundle", err)
	}
	observe(session.ID, sessions.TurnCompleted, "", "")
}
