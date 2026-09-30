package skillpg_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/adminaudit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgtest"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/skills"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/writeaudit"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// auditMutation is one Skill write and the audit record it must leave.
type auditMutation struct {
	action, kind, parent string
	owners               int
	run                  func(context.Context) (string, error)
}

var auditMutations = []string{"skill_create", "skill_upload_version", "skill_update_default", "skill_delete", "version_delete", "version_delete_last"}

func prepareAuditMutation(t *testing.T, f fixture, tenant, name string, bundle []byte) auditMutation {
	t.Helper()
	ctx := t.Context()
	if name == "skill_create" {
		return auditMutation{action: "create", kind: "skill", owners: 2, run: func(ctx context.Context) (string, error) {
			v, e := f.service.CreateSkill(ctx, skills.CreateSkill{TenantID: tenant, Archive: bundle})
			return v.ID, e
		}}
	}
	created, err := f.service.CreateSkill(ctx, skills.CreateSkill{TenantID: tenant, Archive: bundle})
	if err != nil {
		t.Fatal(err)
	}
	id := key(t, created.ID)
	switch name {
	case "skill_upload_version":
		return auditMutation{action: "upload_version", kind: "skill_version", parent: created.ID, owners: 1, run: func(ctx context.Context) (string, error) {
			v, e := f.service.CreateVersion(ctx, skills.CreateVersion{TenantID: tenant, SkillID: id, Archive: bundle, MakeDefault: true})
			return v.ID, e
		}}
	case "skill_delete":
		return auditMutation{action: "delete", kind: "skill", run: func(ctx context.Context) (string, error) {
			return created.ID, f.service.DeleteSkill(ctx, skills.DeleteSkill{TenantID: tenant, SkillID: id})
		}}
	case "version_delete_last":
		return auditMutation{action: "delete", kind: "skill_version", parent: created.ID, run: func(ctx context.Context) (string, error) {
			v, e := f.service.DeleteVersion(ctx, skills.DeleteVersion{TenantID: tenant, SkillID: id, Version: 1})
			return v.ID, e
		}}
	}
	if _, err = f.service.CreateVersion(ctx, skills.CreateVersion{TenantID: tenant, SkillID: id, Archive: bundle}); err != nil {
		t.Fatal(err)
	}
	if name == "skill_update_default" {
		return auditMutation{action: "update_default_version", kind: "skill", run: func(ctx context.Context) (string, error) {
			v, e := f.service.SetDefaultVersion(ctx, skills.SetDefaultVersion{TenantID: tenant, SkillID: id, Version: "2"})
			return v.ID, e
		}}
	}
	return auditMutation{action: "delete", kind: "skill_version", parent: created.ID, run: func(ctx context.Context) (string, error) {
		v, e := f.service.DeleteVersion(ctx, skills.DeleteVersion{TenantID: tenant, SkillID: id, Version: 2})
		return v.ID, e
	}}
}

func writeAuditContext(ctx context.Context, tenant, request string) context.Context {
	return writeaudit.WithSource(ctx, writeaudit.Source{
		KeyID: "static:" + strings.Repeat("a", 64), Name: "resource audit fixture", Prefix: "aaaaaaaa",
		Kind: "static", TenantID: tenant, RequestID: request, TraceID: "resource-audit-trace",
	})
}

// snapshot returns every row of tables, or only the tenant's rows when tenant
// is set, so a comparison covers ciphertext, counters and timestamps.
func snapshot(t *testing.T, pool *pgxpool.Pool, tenant string, tables ...string) map[string]string {
	t.Helper()
	result := make(map[string]string, len(tables))
	for _, table := range tables {
		query := "SELECT COALESCE(jsonb_agg(to_jsonb(r) ORDER BY to_jsonb(r)::text)::text, '[]') FROM " + pgx.Identifier{table}.Sanitize() + " r"
		var args []any
		if tenant != "" {
			query, args = query+" WHERE tenant_id=$1", []any{tenant}
		}
		var rows string
		if err := pool.QueryRow(t.Context(), query, args...).Scan(&rows); err != nil {
			t.Fatalf("snapshot %s: %v", table, err)
		}
		result[table] = rows
	}
	return result
}

// A database trigger fails the final audit insertion after each real Skill
// write. Comparing complete tenant rows proves the write rolled back.
func TestWriteAuditTransactions(t *testing.T) {
	pool := pgtest.OpenIsolated(t, nil)
	f := newFixture(t, pool, testCipher(t, 91))
	ctx := t.Context()
	if _, err := pool.Exec(ctx, `CREATE FUNCTION reject_resource_audit_fixture() RETURNS trigger LANGUAGE plpgsql AS $$
	BEGIN IF NEW.request_id = 'reject-resource-audit' THEN RAISE EXCEPTION 'forced audit insertion failure'; END IF; RETURN NEW; END $$;
	CREATE TRIGGER reject_resource_audit_fixture BEFORE INSERT ON write_audit_operations FOR EACH ROW EXECUTE FUNCTION reject_resource_audit_fixture()`); err != nil {
		t.Fatal(err)
	}
	bundle := proofArchive(t, "audit-private-archive")
	tables := []string{"skills", "skill_versions", "write_audit_operations", "write_audit_owners"}
	for _, name := range auditMutations {
		t.Run(name, func(t *testing.T) {
			tenant := uuid.NewString()
			mutation := prepareAuditMutation(t, f, tenant, name, bundle)
			before := snapshot(t, pool, tenant, tables...)
			if _, err := mutation.run(writeAuditContext(ctx, tenant, "reject-resource-audit")); err == nil {
				t.Fatal("audit failure was accepted")
			}
			if !reflect.DeepEqual(before, snapshot(t, pool, tenant, tables...)) {
				t.Fatal("audit failure left business or audit changes")
			}
			request := uuid.NewString()
			id, err := mutation.run(writeAuditContext(ctx, tenant, request))
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
			rows := snapshot(t, pool, tenant, "write_audit_operations", "write_audit_owners")
			for _, table := range rows {
				if strings.Contains(table, "audit-private-archive") {
					t.Fatal("audit contains the archive")
				}
			}
		})
	}
}

const rejectedAdminRequest = "reject-admin-mutation-fixture"

// Administrator provenance takes precedence over an inherited public source:
// each deletion records one administrator audit row and no public-key
// operation, and a failed administrator audit rolls the deletion back.
func TestAdminDeleteAuditTransactions(t *testing.T) {
	pool := pgtest.OpenIsolated(t, nil)
	f := newFixture(t, pool, testCipher(t, 94))
	ctx := t.Context()
	if _, err := pool.Exec(ctx, `CREATE FUNCTION reject_admin_mutation_fixture() RETURNS trigger LANGUAGE plpgsql AS $$
 BEGIN IF NEW.request_id = 'reject-admin-mutation-fixture' THEN RAISE EXCEPTION 'forced administrator audit failure'; END IF; RETURN NEW; END $$;
 CREATE TRIGGER reject_admin_mutation_fixture BEFORE INSERT ON admin_audit_log FOR EACH ROW EXECUTE FUNCTION reject_admin_mutation_fixture()`); err != nil {
		t.Fatal(err)
	}
	bundle := proofArchive(t, "admin-private-archive")
	tables := []string{"skills", "skill_versions", "admin_audit_log", "write_audit_operations", "write_audit_owners"}
	adminContext := func(tenant, request string) context.Context {
		return adminaudit.WithSource(writeAuditContext(ctx, tenant, request), adminaudit.Source{
			CredentialID: "87654321", ActorLabel: "administrator fixture", ProjectID: tenant, RequestID: request, TraceID: "admin-mutation-trace",
		})
	}
	for _, name := range []string{"skill_delete", "version_delete", "version_delete_last"} {
		t.Run(name, func(t *testing.T) {
			tenant := uuid.NewString()
			mutation := prepareAuditMutation(t, f, tenant, name, bundle)
			if _, err := pool.Exec(ctx, "INSERT INTO execution_project_scopes(tenant_id,organization_id,project_id) VALUES($1,'admin-delete',$2)", tenant, tenant); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, "INSERT INTO projects(id,name,tenant_id,subject_kind,subject_id) VALUES($1,'Delete fixture',$1,'service_account',$2)", tenant, "project:"+tenant); err != nil {
				t.Fatal(err)
			}
			before := snapshot(t, pool, "", tables...)
			if _, err := mutation.run(adminContext(tenant, rejectedAdminRequest)); err == nil {
				t.Fatal("administrator audit failure was accepted")
			}
			if !reflect.DeepEqual(before, snapshot(t, pool, "", tables...)) {
				t.Fatal("failed administrator audit changed Skill or audit rows")
			}
			request := uuid.NewString()
			id, err := mutation.run(adminContext(tenant, request))
			if err != nil {
				t.Fatal(err)
			}
			var credential, actor, project, trace, action, kind, gotID, mappings, raw string
			if err := pool.QueryRow(ctx, `SELECT admin_credential_id,actor_label,project_id,trace_id,action,resource_type,resource_id,result_ids::text,to_jsonb(a)::text
 FROM admin_audit_log a WHERE tenant_id=$1 AND request_id=$2`, tenant, request).Scan(&credential, &actor, &project, &trace, &action, &kind, &gotID, &mappings, &raw); err != nil {
				t.Fatal(err)
			}
			if credential != "87654321" || actor != "administrator fixture" || project != tenant || trace != "admin-mutation-trace" || action != "delete" || kind != mutation.kind || gotID != id || mappings != "[]" {
				t.Fatal("administrator audit identity differs")
			}
			if strings.Contains(raw, "admin-private-archive") {
				t.Fatal("private content entered administrator audit")
			}
			var audits, operations, owners int
			if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM admin_audit_log WHERE tenant_id=$1 AND request_id=$2),(SELECT count(*) FROM write_audit_operations WHERE tenant_id=$1),(SELECT count(*) FROM write_audit_owners WHERE tenant_id=$1)`, tenant, request).Scan(&audits, &operations, &owners); err != nil {
				t.Fatal(err)
			}
			if audits != 1 || operations != 0 || owners != 0 {
				t.Fatal("administrator impersonated public-key provenance")
			}
			var remaining int
			if err := pool.QueryRow(ctx, "SELECT count(*) FROM skill_versions WHERE tenant_id=$1 AND 'skillver_' || id::text = $2", tenant, id).Scan(&remaining); err != nil || remaining != 0 {
				t.Fatal("deleted version survived", remaining, err)
			}
			if kind == "skill" {
				if _, err := f.store.Skill(ctx, tenant, key(t, id)); err == nil {
					t.Fatal("deleted Skill survived")
				}
			}
		})
	}
}
