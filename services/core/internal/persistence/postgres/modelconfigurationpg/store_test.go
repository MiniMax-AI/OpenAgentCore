package modelconfigurationpg_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/google/uuid"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/adminaudit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/modelconfiguration"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/textvalue"
)

func TestDefaultsAreEncryptedAuditedAndReplaced(t *testing.T) {
	f := newFixture(t)
	provider := v1.ModelProviderInput{Protocol: "responses", BaseURL: "https://deployment.example/v1", APIKey: "deployment-key-canary"}
	saved := f.replace(t, provider)
	if saved.Harness != "codex" || saved.Provider != *provider.SafeView() || saved.UpdatedAt.IsZero() {
		t.Fatal("default not saved", saved)
	}
	var row []byte
	if err := f.pool.QueryRow(t.Context(), "SELECT to_jsonb(p)::text FROM deployment_model_providers p WHERE harness='codex'").Scan(&row); err != nil || bytes.Contains(row, []byte("deployment-key-canary")) {
		t.Fatal("key stored in plaintext", err)
	}
	if listed := f.list(t); len(listed) != 1 || listed[0].Provider != *provider.SafeView() {
		t.Fatal("safe fields not listed", listed)
	}
	if got := f.resolve(t); got == nil || *got.Provider != provider {
		t.Fatal("default did not open", got)
	}
	if got, err := f.service.Resolve(t.Context(), "mcode"); err != nil || got != nil {
		t.Fatal("unset harness returned a default", err)
	}
	before := f.resolve(t).Revision
	replacement := v1.ModelProviderInput{Protocol: "responses", BaseURL: "https://replacement.example/v1", APIKey: "replacement-key"}
	f.replace(t, replacement)
	if got := f.resolve(t); *got.Provider != replacement || got.Revision == before {
		t.Fatal("replacement not complete", got)
	}
	for range 2 {
		if err := f.service.Delete(admin(t), "codex"); err != nil {
			t.Fatal("delete is not idempotent", err)
		}
	}
	if got := f.resolve(t); got != nil {
		t.Fatal("deleted default remained")
	}
	rows, err := f.pool.Query(t.Context(), "SELECT action, project_id IS NULL AND tenant_id IS NULL, resource_id FROM admin_audit_log WHERE resource_type='deployment_model_provider' ORDER BY created_at")
	if err != nil {
		t.Fatal(err)
	}
	var actions []string
	for rows.Next() {
		var action, resource string
		var deploymentWide bool
		if err := rows.Scan(&action, &deploymentWide, &resource); err != nil || !deploymentWide || resource != "codex" {
			t.Fatal("unexpected deployment audit entry", action, resource, err)
		}
		actions = append(actions, action)
	}
	if rows.Err() != nil || len(actions) != 4 || actions[0] != "set" || actions[3] != "delete" {
		t.Fatal("deployment writes not audited", actions, rows.Err())
	}
}

func TestRejectedWritesStoreNothing(t *testing.T) {
	f := newFixture(t)
	scoped := adminaudit.WithSource(t.Context(), adminaudit.Source{CredentialID: "abcd1234", RequestID: "r", TraceID: "t", ProjectID: uuid.NewString()})
	configuration := v1.ModelConfigurationInput{ModelProvider: fixtureProvider, Model: "fixture"}
	for name, ctx := range map[string]context.Context{"project-scoped source": scoped, "no source": t.Context()} {
		if _, err := f.service.Replace(ctx, modelconfiguration.Replacement{Harness: "codex", Configuration: configuration}); !errors.Is(err, adminaudit.ErrInvalidSource) {
			t.Fatal(name, "accepted for a deployment write", err)
		}
	}
	unstorable := configuration
	unstorable.Model = "fixture\x00model"
	if _, err := f.service.Replace(admin(t), modelconfiguration.Replacement{Harness: "codex", Configuration: unstorable}); !errors.Is(err, textvalue.ErrUnstorable) {
		t.Fatal("unstorable text", err)
	}
	var stored, audited int
	if err := f.pool.QueryRow(t.Context(), "SELECT (SELECT count(*) FROM deployment_model_providers), (SELECT count(*) FROM admin_audit_log)").Scan(&stored, &audited); err != nil || stored != 0 || audited != 0 {
		t.Fatal("rejected write committed", stored, audited, err)
	}
}
