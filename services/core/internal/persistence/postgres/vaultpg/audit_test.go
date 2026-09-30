package vaultpg_test

import (
	"bytes"
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/adminaudit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgtest"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/vaultpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/vaults"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/writeaudit"
)

const rejectedAudit = "reject-vault-audit"

// auditMutation is one audited write, prepared against its own tenant.
type auditMutation struct {
	action, kind, parent string
	owners               int
	run                  func(context.Context) (string, error)
}

func publicAuditContext(ctx context.Context, tenant, request string) context.Context {
	return writeaudit.WithSource(ctx, writeaudit.Source{
		KeyID: "static:" + strings.Repeat("a", 64), Name: "vault audit fixture", Prefix: "aaaaaaaa",
		Kind: "static", TenantID: tenant, RequestID: request, TraceID: "vault-audit-trace",
	})
}

// adminAuditContext keeps an inherited public source, which must not turn an
// administrator operation into a user-key operation.
func adminAuditContext(ctx context.Context, tenant, request string) context.Context {
	return adminaudit.WithSource(publicAuditContext(ctx, tenant, request), adminaudit.Source{
		CredentialID: "87654321", ActorLabel: "administrator fixture", ProjectID: tenant, RequestID: request, TraceID: "admin-mutation-trace",
	})
}

// rejectAudits fails every audit insertion made for the rejected request.
// The sequence survives rollback, so it proves the insertion was reached even
// though the Store reports only a sanitized failure.
func rejectAudits(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	_, err := pool.Exec(t.Context(), `CREATE SEQUENCE vault_audit_rejections;
 CREATE FUNCTION reject_vault_audit() RETURNS trigger LANGUAGE plpgsql AS $$
 BEGIN IF NEW.request_id = '`+rejectedAudit+`' THEN PERFORM nextval('vault_audit_rejections'); RAISE EXCEPTION 'forced audit insertion failure'; END IF; RETURN NEW; END $$;
 CREATE TRIGGER reject_vault_audit BEFORE INSERT ON write_audit_operations FOR EACH ROW EXECUTE FUNCTION reject_vault_audit();
 CREATE TRIGGER reject_vault_audit BEFORE INSERT ON admin_audit_log FOR EACH ROW EXECUTE FUNCTION reject_vault_audit()`)
	if err != nil {
		t.Fatal(err)
	}
}

func auditRejections(t *testing.T, pool *pgxpool.Pool) int64 {
	t.Helper()
	var count int64
	if err := pool.QueryRow(t.Context(), "SELECT CASE WHEN is_called THEN last_value ELSE 0 END FROM vault_audit_rejections").Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

// auditSnapshot covers the isolated database's complete rows, including
// ciphertext and timestamps.
func auditSnapshot(t *testing.T, pool *pgxpool.Pool) map[string]string {
	t.Helper()
	result := map[string]string{}
	for _, table := range []string{"vaults", "vault_credentials", "write_audit_operations", "write_audit_owners", "admin_audit_log"} {
		var rows string
		if err := pool.QueryRow(t.Context(), "SELECT COALESCE(jsonb_agg(to_jsonb(r) ORDER BY to_jsonb(r)::text)::text,'[]') FROM "+pgx.Identifier{table}.Sanitize()+" r").Scan(&rows); err != nil {
			t.Fatalf("snapshot %s: %v", table, err)
		}
		result[table] = rows
	}
	return result
}

func prepareAuditMutation(t *testing.T, service *vaults.Service, tenant, name string) auditMutation {
	t.Helper()
	ctx := t.Context()
	if name == "vault_create" {
		return auditMutation{action: "create", kind: "vault", owners: 1, run: func(ctx context.Context) (string, error) {
			v, err := service.CreateVault(ctx, vaults.CreateVault{TenantID: tenant})
			return v.ID, err
		}}
	}
	vault := createVault(t, service, tenant)
	static := vaults.CreateStaticCredential{TenantID: tenant, VaultID: vault.ID, Name: "fixture", MCPServerURL: "https://mcp.example/", Token: "audit-private-token"}
	remove := func(id string) auditMutation {
		return auditMutation{action: "delete", kind: "credential", parent: vault.ID, run: func(ctx context.Context) (string, error) {
			return service.DeleteCredential(ctx, vaults.DeleteCredential{TenantID: tenant, VaultID: vault.ID, CredentialID: id})
		}}
	}
	switch name {
	case "credential_create":
		return auditMutation{action: "create", kind: "credential", parent: vault.ID, owners: 1, run: func(ctx context.Context) (string, error) {
			v, err := service.CreateStaticCredential(ctx, static)
			return v.ID, err
		}}
	case "oauth_create", "oauth_update", "oauth_delete":
		command := vaults.CreateOAuthCredential{TenantID: tenant, VaultID: vault.ID, Name: "fixture", MCPServerURL: static.MCPServerURL, AccessToken: static.Token}
		if name == "oauth_create" {
			return auditMutation{action: "create", kind: "credential", parent: vault.ID, owners: 1, run: func(ctx context.Context) (string, error) {
				v, err := service.CreateOAuthCredential(ctx, command)
				return v.ID, err
			}}
		}
		credential, err := service.CreateOAuthCredential(ctx, command)
		if err != nil {
			t.Fatal(err)
		}
		if name == "oauth_delete" {
			return remove(credential.ID)
		}
		return auditMutation{action: "update", kind: "credential", parent: vault.ID, run: func(ctx context.Context) (string, error) {
			v, err := service.UpdateOAuthCredential(ctx, vaults.UpdateOAuthCredential{TenantID: tenant, VaultID: vault.ID, CredentialID: credential.ID, AccessToken: ptr("audit-private-replacement")})
			return v.ID, err
		}}
	}
	credential := createStatic(t, service, tenant, vault.ID, static.Name, static.MCPServerURL, static.Token)
	switch name {
	case "vault_delete":
		return auditMutation{action: "delete", kind: "vault", run: func(ctx context.Context) (string, error) {
			return service.DeleteVault(ctx, vaults.DeleteVault{TenantID: tenant, VaultID: vault.ID})
		}}
	case "credential_update":
		return auditMutation{action: "update", kind: "credential", parent: vault.ID, run: func(ctx context.Context) (string, error) {
			v, err := service.UpdateStaticCredential(ctx, vaults.UpdateStaticCredential{TenantID: tenant, VaultID: vault.ID, CredentialID: credential.ID, Token: "audit-private-replacement"})
			return v.ID, err
		}}
	}
	return remove(credential.ID)
}

// A trigger fails the final audit insertion after each real mutation. Complete
// table snapshots prove the rollback of ciphertext, timestamps and cascades.
func TestVaultMutationsRollBackWithTheirAudit(t *testing.T) {
	pool := pgtest.OpenIsolated(t, nil)
	service := newService(t, vaultpg.New(pgunit.NewPool(pool)), newCipher(t, bytes.Repeat([]byte{91}, 32)), nil)
	rejectAudits(t, pool)
	secrets := []string{"audit-private-token", "audit-private-replacement"}
	for _, provenance := range []string{"public", "admin"} {
		names := []string{"vault_create", "vault_delete", "credential_create", "credential_update", "credential_delete", "oauth_create", "oauth_update", "oauth_delete"}
		source := publicAuditContext
		if provenance == "admin" {
			// Administrators only delete Vaults and Credentials.
			names, source = []string{"vault_delete", "credential_delete", "oauth_delete"}, adminAuditContext
		}
		for _, name := range names {
			t.Run(provenance+"/"+name, func(t *testing.T) {
				tenant := uuid.NewString()
				mutation := prepareAuditMutation(t, service, tenant, name)
				if provenance == "admin" {
					// The administrator audit references the Project, which
					// references its execution scope.
					if _, err := pool.Exec(t.Context(), "INSERT INTO execution_project_scopes(tenant_id,organization_id,project_id) VALUES($1,'admin-delete',$2)", tenant, tenant); err != nil {
						t.Fatal(err)
					}
					if _, err := pool.Exec(t.Context(), "INSERT INTO projects(id,name,tenant_id,subject_kind,subject_id) VALUES($1,'Delete fixture',$1,'service_account',$2)", tenant, "project:"+tenant); err != nil {
						t.Fatal(err)
					}
				}
				before, rejections := auditSnapshot(t, pool), auditRejections(t, pool)
				if _, err := mutation.run(source(t.Context(), tenant, rejectedAudit)); err == nil || auditRejections(t, pool) != rejections+1 {
					t.Fatal("mutation did not reach the failing audit insertion", err)
				}
				if !reflect.DeepEqual(before, auditSnapshot(t, pool)) {
					t.Fatal("audit failure left business or audit changes")
				}
				request := uuid.NewString()
				id, err := mutation.run(source(t.Context(), tenant, request))
				if err != nil {
					t.Fatal(err)
				}
				var public, admin, owners int
				if err := pool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM write_audit_operations WHERE tenant_id=$1 AND request_id=$2),
 (SELECT count(*) FROM admin_audit_log WHERE tenant_id=$1 AND request_id=$2), (SELECT count(*) FROM write_audit_owners WHERE tenant_id=$1)`, tenant, request).Scan(&public, &admin, &owners); err != nil {
					t.Fatal(err)
				}
				var action, kind, gotID, parent, raw string
				if provenance == "public" {
					if public != 1 || admin != 0 || owners != mutation.owners {
						t.Fatalf("public audit rows %d, admin rows %d, owners %d", public, admin, owners)
					}
					if err := pool.QueryRow(t.Context(), `SELECT action,resource_type,resource_id,COALESCE(parent_id,''),to_jsonb(o)::text FROM write_audit_operations o WHERE tenant_id=$1 AND request_id=$2`, tenant, request).Scan(&action, &kind, &gotID, &parent, &raw); err != nil {
						t.Fatal(err)
					}
				} else {
					// An administrator deletion never records public-key provenance.
					if public != 0 || admin != 1 || owners != 0 {
						t.Fatalf("public audit rows %d, admin rows %d, owners %d", public, admin, owners)
					}
					var credential, actor, project, trace, results string
					if err := pool.QueryRow(t.Context(), `SELECT admin_credential_id,actor_label,project_id,trace_id,result_ids::text,action,resource_type,resource_id,to_jsonb(a)::text FROM admin_audit_log a WHERE tenant_id=$1 AND request_id=$2`, tenant, request).Scan(&credential, &actor, &project, &trace, &results, &action, &kind, &gotID, &raw); err != nil {
						t.Fatal(err)
					}
					if credential != "87654321" || actor != "administrator fixture" || project != tenant || trace != "admin-mutation-trace" || results != "[]" {
						t.Fatal("administrator audit identity differs")
					}
					parent = mutation.parent
				}
				if action != mutation.action || kind != mutation.kind || gotID != id || parent != mutation.parent {
					t.Fatalf("wrong operation identity: %s %s %s %s", action, kind, gotID, parent)
				}
				for _, secret := range secrets {
					if strings.Contains(raw, secret) {
						t.Fatal("audit contains a secret")
					}
				}
			})
		}
	}
}
