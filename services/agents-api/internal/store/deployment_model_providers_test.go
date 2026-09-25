package store

import (
	"bytes"
	"errors"
	"testing"
	"time"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/adminaudit"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/credentialcrypto"
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
	if _, err := s.SetDeploymentModelProvider(ctx, "codex", v1.ModelProviderInput{Protocol: "anthropic", BaseURL: provider.BaseURL, APIKey: "k"}); !errors.Is(err, ErrInvalidInput) {
		t.Fatal("protocol unsupported by the harness accepted", err)
	}
	if _, err := New(pool).SetDeploymentModelProvider(ctx, "codex", provider); !errors.Is(err, ErrCredentialStorageUnavailable) {
		t.Fatal("key stored without encryption", err)
	}
	saved, err := s.SetDeploymentModelProvider(ctx, "codex", provider)
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
	if got, err := s.DeploymentModelProvider(ctx, "codex"); err != nil || got == nil || *got != provider {
		t.Fatal("default did not decrypt", err)
	}
	if got, err := s.DeploymentModelProvider(ctx, "mcode"); err != nil || got != nil {
		t.Fatal("unset harness returned a default", err)
	}
	other, _ := credentialcrypto.New(bytes.Repeat([]byte{48}, 32))
	if _, err := NewWithCredentialCipher(pool, other).DeploymentModelProvider(ctx, "codex"); !errors.Is(err, ErrCredentialStorageUnavailable) {
		t.Fatal("wrong encryption key did not fail closed", err)
	}
	replacement := v1.ModelProviderInput{Protocol: "responses", BaseURL: "https://replacement.example/v1", APIKey: "replacement-key"}
	if _, err := s.SetDeploymentModelProvider(ctx, "codex", replacement); err != nil {
		t.Fatal(err)
	}
	if got, err := s.DeploymentModelProvider(ctx, "codex"); err != nil || *got != replacement {
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
	page, err := s.ListAdminAudit(ctx, AdminAuditFilter{ResourceType: "deployment_model_provider", CreatedAfter: &started})
	if err != nil || len(page.Data) != 4 {
		t.Fatal("deployment writes not audited", len(page.Data), err)
	}
	for _, entry := range page.Data {
		if entry.ProjectID != nil || entry.ResourceID != "codex" || (entry.Action != "set" && entry.Action != "delete") {
			t.Fatal("unexpected deployment audit entry", entry)
		}
	}
	if scoped, err := s.ListAdminAudit(ctx, AdminAuditFilter{ProjectID: "00000000-0000-4000-8000-000000000001"}); err != nil || len(scoped.Data) != 0 {
		t.Fatal("Project filter returned deployment entries", err)
	}
	if _, err := s.SetDeploymentModelProvider(adminaudit.WithSource(t.Context(), adminaudit.Source{CredentialID: "abcd1234", RequestID: "r", TraceID: "t", ProjectID: "00000000-0000-4000-8000-000000000001"}), "codex", provider); !errors.Is(err, ErrInvalidInput) {
		t.Fatal("Project-scoped audit source accepted for a deployment write", err)
	}
	if got, _ := s.DeploymentModelProvider(ctx, "codex"); got != nil {
		t.Fatal("failed audit left the write committed")
	}
}
