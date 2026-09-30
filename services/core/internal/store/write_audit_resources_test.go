package store

import (
	"bytes"
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/writeaudit"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type resourceAuditMutation struct {
	action, kind, parent string
	owners               int
	run                  func(context.Context) (string, error)
}

func resourceAuditContext(ctx context.Context, tenant, request string) context.Context {
	return writeaudit.WithSource(ctx, writeaudit.Source{
		KeyID: "static:" + strings.Repeat("a", 64), Name: "resource audit fixture", Prefix: "aaaaaaaa",
		Kind: "static", TenantID: tenant, RequestID: request, TraceID: "resource-audit-trace",
	})
}

// A database trigger fails the final audit insertion after each real business
// mutation. Comparing complete tenant rows proves rollback of secret ciphertext,
// version counters, timestamps, and cascades.
func TestWriteAuditStandaloneResourceTransactions(t *testing.T) {
	_, pool := newManagedTestStore(t)
	cipher, err := credentialcrypto.New(bytes.Repeat([]byte{91}, 32))
	if err != nil {
		t.Fatal(err)
	}
	s := NewWithCredentialCipher(pool, cipher)
	ctx := t.Context()
	_, err = pool.Exec(ctx, `CREATE FUNCTION reject_resource_audit_fixture() RETURNS trigger LANGUAGE plpgsql AS $$
	BEGIN IF NEW.request_id = 'reject-resource-audit' THEN RAISE EXCEPTION 'forced audit insertion failure'; END IF; RETURN NEW; END $$;
	CREATE TRIGGER reject_resource_audit_fixture BEFORE INSERT ON write_audit_operations FOR EACH ROW EXECUTE FUNCTION reject_resource_audit_fixture()`)
	if err != nil {
		t.Fatal(err)
	}
	archive := skillArchive(t, "audit-private-archive")
	for _, name := range []string{
		"template_create", "template_update", "template_delete",
		"skill_create", "skill_upload_version", "skill_update_default", "skill_delete", "version_delete", "version_delete_last",
		"vault_create", "vault_delete", "credential_create", "credential_update", "credential_delete",
		"oauth_create", "oauth_update", "oauth_delete",
	} {
		t.Run(name, func(t *testing.T) {
			tenant := uuid.NewString()
			mutation := prepareResourceAuditMutation(t, s, tenant, name, archive)
			snapshot := func() map[string]string {
				result := make(map[string]string)
				for _, table := range []string{"agents", "environment_templates", "skills", "skill_versions", "vaults", "vault_credentials", "write_audit_operations", "write_audit_owners"} {
					query := "SELECT COALESCE(jsonb_agg(to_jsonb(r) ORDER BY to_jsonb(r)::text)::text, '[]') FROM " + pgx.Identifier{table}.Sanitize() + " r WHERE tenant_id=$1"
					if table == "vault_credentials" {
						query = "SELECT COALESCE(jsonb_agg(to_jsonb(r) ORDER BY r.id)::text, '[]') FROM vault_credentials r JOIN vaults v ON v.id=r.vault_id WHERE v.tenant_id=$1"
					}
					var value string
					if err := pool.QueryRow(ctx, query, tenant).Scan(&value); err != nil {
						t.Fatalf("snapshot %s: %v", table, err)
					}
					result[table] = value
				}
				return result
			}
			before := snapshot()
			if _, err := mutation.run(resourceAuditContext(ctx, tenant, "reject-resource-audit")); err == nil {
				t.Fatal("audit failure was accepted")
			}
			if !reflect.DeepEqual(before, snapshot()) {
				t.Fatal("audit failure left business or audit changes")
			}
			request := uuid.NewString()
			id, err := mutation.run(resourceAuditContext(ctx, tenant, request))
			if err != nil {
				t.Fatal(err)
			}
			var action, kind, gotID string
			var parent *string
			if err := pool.QueryRow(ctx, `SELECT action,resource_type,resource_id,parent_id FROM write_audit_operations WHERE tenant_id=$1 AND request_id=$2`, tenant, request).Scan(&action, &kind, &gotID, &parent); err != nil {
				t.Fatal(err)
			}
			gotParent := ""
			if parent != nil {
				gotParent = *parent
			}
			if action != mutation.action || kind != mutation.kind || gotID != id || gotParent != mutation.parent {
				t.Fatalf("wrong operation identity: %s %s %s %s", action, kind, gotID, gotParent)
			}
			var owners int
			if err := pool.QueryRow(ctx, `SELECT count(*) FROM write_audit_owners WHERE tenant_id=$1`, tenant).Scan(&owners); err != nil || owners != mutation.owners {
				t.Fatalf("ownership count %d, want %d: %v", owners, mutation.owners, err)
			}
			rows := snapshot()
			for _, table := range []string{"write_audit_operations", "write_audit_owners"} {
				for _, secret := range []string{"audit-private-archive", "audit-private-token", "audit-private-replacement"} {
					if strings.Contains(rows[table], secret) {
						t.Fatal("audit contains secret")
					}
				}
			}
		})
	}
}

func prepareResourceAuditMutation(t *testing.T, s *Store, tenant, name string, archive []byte) resourceAuditMutation {
	t.Helper()
	ctx := t.Context()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	if strings.HasPrefix(name, "template_") {
		if name == "template_create" {
			return resourceAuditMutation{action: "create", kind: "environment_template", owners: 1, run: func(ctx context.Context) (string, error) {
				v, e := s.CreateEnvironmentTemplate(ctx, tenant, EnvironmentTemplateInput{})
				return v.ID, e
			}}
		}
		v, err := s.CreateEnvironmentTemplate(ctx, tenant, EnvironmentTemplateInput{})
		must(err)
		if name == "template_update" {
			return resourceAuditMutation{action: "update", kind: "environment_template", run: func(ctx context.Context) (string, error) {
				label := "replacement"
				v, e := s.UpdateEnvironmentTemplate(ctx, tenant, v.ID, EnvironmentTemplateInput{SetName: true, Name: &label})
				return v.ID, e
			}}
		}
		return resourceAuditMutation{action: "delete", kind: "environment_template", run: func(ctx context.Context) (string, error) { return s.DeleteEnvironmentTemplate(ctx, tenant, v.ID) }}
	}
	if strings.HasPrefix(name, "skill_") || strings.HasPrefix(name, "version_") {
		if name == "skill_create" {
			return resourceAuditMutation{action: "create", kind: "skill", owners: 2, run: func(ctx context.Context) (string, error) { v, e := s.CreateSkill(ctx, tenant, archive); return v.ID, e }}
		}
		v, err := s.CreateSkill(ctx, tenant, archive)
		must(err)
		if name == "skill_upload_version" {
			return resourceAuditMutation{action: "upload_version", kind: "skill_version", parent: v.ID, owners: 1, run: func(ctx context.Context) (string, error) {
				v, e := s.CreateSkillVersion(ctx, tenant, v.ID, archive, true)
				return v.ID, e
			}}
		}
		if name == "skill_delete" {
			return resourceAuditMutation{action: "delete", kind: "skill", run: func(ctx context.Context) (string, error) { return v.ID, s.DeleteSkill(ctx, tenant, v.ID) }}
		}
		if name == "version_delete_last" {
			return resourceAuditMutation{action: "delete", kind: "skill_version", parent: v.ID, run: func(ctx context.Context) (string, error) {
				v, e := s.DeleteSkillVersion(ctx, tenant, v.ID, "1")
				return v.ID, e
			}}
		}
		_, err = s.CreateSkillVersion(ctx, tenant, v.ID, archive, false)
		must(err)
		if name == "skill_update_default" {
			return resourceAuditMutation{action: "update_default_version", kind: "skill", run: func(ctx context.Context) (string, error) {
				v, e := s.UpdateSkillDefault(ctx, tenant, v.ID, "2")
				return v.ID, e
			}}
		}
		return resourceAuditMutation{action: "delete", kind: "skill_version", parent: v.ID, run: func(ctx context.Context) (string, error) {
			v, e := s.DeleteSkillVersion(ctx, tenant, v.ID, "2")
			return v.ID, e
		}}
	}
	if name == "vault_create" {
		return resourceAuditMutation{action: "create", kind: "vault", owners: 1, run: func(ctx context.Context) (string, error) {
			v, e := s.CreateVault(ctx, tenant, CreateVaultInput{})
			return v.ID, e
		}}
	}
	vault, err := s.CreateVault(ctx, tenant, CreateVaultInput{})
	must(err)
	static := CreateStaticCredentialInput{Name: "fixture", MCPServerURL: "https://mcp.example/", Token: "audit-private-token"}
	if name == "credential_create" {
		return resourceAuditMutation{action: "create", kind: "credential", parent: vault.ID, owners: 1, run: func(ctx context.Context) (string, error) {
			v, e := s.CreateStaticCredential(ctx, tenant, vault.ID, static)
			return v.ID, e
		}}
	}
	if strings.HasPrefix(name, "oauth_") {
		input := CreateOAuthCredentialInput{Name: "fixture", MCPServerURL: static.MCPServerURL, AccessToken: static.Token}
		if name == "oauth_create" {
			return resourceAuditMutation{action: "create", kind: "credential", parent: vault.ID, owners: 1, run: func(ctx context.Context) (string, error) {
				v, e := s.CreateOAuthCredential(ctx, tenant, vault.ID, input)
				return v.ID, e
			}}
		}
		v, err := s.CreateOAuthCredential(ctx, tenant, vault.ID, input)
		must(err)
		if name == "oauth_update" {
			return resourceAuditMutation{action: "update", kind: "credential", parent: vault.ID, run: func(ctx context.Context) (string, error) {
				token := "audit-private-replacement"
				v, e := s.UpdateOAuthCredential(ctx, tenant, vault.ID, v.ID, UpdateOAuthCredentialInput{AccessToken: &token})
				return v.ID, e
			}}
		}
		return resourceAuditMutation{action: "delete", kind: "credential", parent: vault.ID, run: func(ctx context.Context) (string, error) { return s.DeleteCredential(ctx, tenant, vault.ID, v.ID) }}
	}
	v, err := s.CreateStaticCredential(ctx, tenant, vault.ID, static)
	must(err)
	if name == "vault_delete" {
		return resourceAuditMutation{action: "delete", kind: "vault", run: func(ctx context.Context) (string, error) { return s.DeleteVault(ctx, tenant, vault.ID) }}
	}
	if name == "credential_update" {
		return resourceAuditMutation{action: "update", kind: "credential", parent: vault.ID, run: func(ctx context.Context) (string, error) {
			v, e := s.UpdateStaticCredential(ctx, tenant, vault.ID, v.ID, UpdateStaticCredentialInput{Token: "audit-private-replacement"})
			return v.ID, e
		}}
	}
	return resourceAuditMutation{action: "delete", kind: "credential", parent: vault.ID, run: func(ctx context.Context) (string, error) { return s.DeleteCredential(ctx, tenant, vault.ID, v.ID) }}
}

// A public mutation owns its transaction, so inject database failures through
// the connection configuration rather than replacing a Store query wrapper.
func readOnlyResourceStore(t *testing.T, pool *pgxpool.Pool) *Store {
	t.Helper()
	config := pool.Config().Copy()
	config.ConnConfig.RuntimeParams["default_transaction_read_only"] = "on"
	readOnly, err := pgxpool.NewWithConfig(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(readOnly.Close)
	return New(readOnly)
}
