package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"
	"time"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/adminaudit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/auditpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/google/uuid"
)

func TestDeploymentModelProviderEncryptedAuditedAndReplaced(t *testing.T) {
	_, pool := testStore(t)
	cipher, err := credentialcrypto.New(bytes.Repeat([]byte{47}, 32))
	if err != nil {
		t.Fatal(err)
	}
	s := NewWithCredentialCipher(pool, cipher)
	// The table is deployment-wide; start from no defaults.
	if _, err := pool.Exec(t.Context(), "DELETE FROM deployment_model_providers"); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	ctx := adminaudit.WithSource(t.Context(), adminaudit.Source{CredentialID: "abcd1234", ActorLabel: "console", RequestID: "request-set", TraceID: "trace-set"})
	provider := v1.ModelProviderInput{Protocol: "responses", BaseURL: "https://deployment.example/v1", APIKey: "deployment-key-canary"}
	if _, err := s.SetDeploymentModelProvider(ctx, "codex", v1.ModelConfigurationInput{ModelProvider: v1.ModelProviderInput{Protocol: "unknown", BaseURL: provider.BaseURL, APIKey: "k"}, Model: "fixture"}); !errors.Is(err, ErrInvalidInput) {
		t.Fatal("unknown upstream protocol accepted", err)
	}
	if _, err := New(pool).SetDeploymentModelProvider(ctx, "codex", v1.ModelConfigurationInput{ModelProvider: provider, Model: "fixture"}); !errors.Is(err, credentialcrypto.ErrUnavailable) {
		t.Fatal("key stored without encryption", err)
	}
	saved, err := s.SetDeploymentModelProvider(ctx, "codex", v1.ModelConfigurationInput{ModelProvider: provider, Model: "fixture"})
	if err != nil || saved.Harness != "codex" || saved.Provider != *provider.SafeView() || saved.UpdatedAt.IsZero() {
		t.Fatal("default not saved", saved, err)
	}
	var row []byte
	if err := pool.QueryRow(ctx, "SELECT to_jsonb(p)::text FROM deployment_model_providers p WHERE harness='codex'").Scan(&row); err != nil || bytes.Contains(row, []byte("deployment-key-canary")) {
		t.Fatal("key stored in plaintext", err)
	}
	listed, err := New(pool).ListDeploymentModelProviders(ctx)
	if err != nil || len(listed) != 1 || listed[0].Provider != *provider.SafeView() {
		t.Fatal("reader without the key could not list safe fields", listed, err)
	}
	if got, err := s.DeploymentModelProvider(ctx, "codex"); err != nil || got == nil || *got.Provider != provider {
		t.Fatal("default did not decrypt", err)
	}
	if got, err := s.DeploymentModelProvider(ctx, "mcode"); err != nil || got != nil {
		t.Fatal("unset harness returned a default", err)
	}
	other, _ := credentialcrypto.New(bytes.Repeat([]byte{48}, 32))
	if _, err := NewWithCredentialCipher(pool, other).DeploymentModelProvider(ctx, "codex"); !errors.Is(err, credentialcrypto.ErrUnavailable) {
		t.Fatal("wrong encryption key did not fail closed", err)
	}
	replacement := v1.ModelProviderInput{Protocol: "responses", BaseURL: "https://replacement.example/v1", APIKey: "replacement-key"}
	if _, err := s.SetDeploymentModelProvider(ctx, "codex", v1.ModelConfigurationInput{ModelProvider: replacement, Model: "fixture"}); err != nil {
		t.Fatal(err)
	}
	if got, err := s.DeploymentModelProvider(ctx, "codex"); err != nil || *got.Provider != replacement {
		t.Fatal("replacement not complete", err)
	}
	deleteCtx := adminaudit.WithSource(t.Context(), adminaudit.Source{CredentialID: "abcd1234", RequestID: "request-delete", TraceID: "trace-delete"})
	for range 2 {
		if err := s.DeleteDeploymentModelProvider(deleteCtx, "codex"); err != nil {
			t.Fatal("delete is not idempotent", err)
		}
	}
	if got, err := s.DeploymentModelProvider(ctx, "codex"); err != nil || got != nil {
		t.Fatal("deleted default remained", err)
	}
	page, err := auditpg.New(pgunit.NewPool(pool)).ListAdminAudit(ctx, adminaudit.Filter{ResourceType: "deployment_model_provider", CreatedAfter: &started})
	if err != nil || len(page.Data) != 4 {
		t.Fatal("deployment writes not audited", len(page.Data), err)
	}
	for _, entry := range page.Data {
		if entry.ProjectID != nil || entry.ResourceID != "codex" || (entry.Action != "set" && entry.Action != "delete") {
			t.Fatal("unexpected deployment audit entry", entry)
		}
	}
	if scoped, err := auditpg.New(pgunit.NewPool(pool)).ListAdminAudit(ctx, adminaudit.Filter{ProjectID: "00000000-0000-4000-8000-000000000001"}); err != nil || len(scoped.Data) != 0 {
		t.Fatal("Project filter returned deployment entries", err)
	}
	if _, err := s.SetDeploymentModelProvider(adminaudit.WithSource(t.Context(), adminaudit.Source{CredentialID: "abcd1234", RequestID: "r", TraceID: "t", ProjectID: "00000000-0000-4000-8000-000000000001"}), "codex", v1.ModelConfigurationInput{ModelProvider: provider, Model: "fixture"}); !errors.Is(err, adminaudit.ErrInvalidSource) {
		t.Fatal("Project-scoped audit source accepted for a deployment write", err)
	}
	if got, _ := s.DeploymentModelProvider(ctx, "codex"); got != nil {
		t.Fatal("failed audit left the write committed")
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
	input := CreateSessionInput{Creator: FixtureCreator(), Engine: "codex", IdempotencyKey: "same-request", ModelProvider: provider, ModelProviderSource: v1.ModelProviderSourceSession,
		Configuration: []byte(`{"agent":{"model":"m"},"environment":{"type":"openai_hosted"}}`), CreationRequest: intent}
	var hashes [2][2]string
	for index, seed := range []byte{71, 72} {
		cipher, err := credentialcrypto.New(bytes.Repeat([]byte{seed}, 32))
		if err != nil {
			t.Fatal(err)
		}
		session, err := NewWithCredentialCipher(pool, cipher).CreateSession(t.Context(), uuid.NewString(), input)
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
	if _, err := NewWithCredentialCipher(pool, cipher).CreateSession(t.Context(), uuid.NewString(), unreadable); !errors.Is(err, ErrInvalidInput) {
		t.Fatal("unreadable provider intent was hashed", err)
	}
}
