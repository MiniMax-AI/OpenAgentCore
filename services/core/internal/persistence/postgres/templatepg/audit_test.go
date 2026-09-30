package templatepg_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/adminaudit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/environmentconfig"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/environmenttemplates"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgtest"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/writeaudit"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const rejectedRequest = "reject-template-audit"

// auditFixture owns an isolated database whose audit tables reject rows with
// rejectedRequest, so the final audit insert of a mutation fails.
func auditFixture(t *testing.T) fixture {
	t.Helper()
	f := newFixture(t, pgtest.OpenIsolated(t, nil))
	_, err := f.pool.Exec(t.Context(), `CREATE FUNCTION reject_template_audit() RETURNS trigger LANGUAGE plpgsql AS $$
	BEGIN IF NEW.request_id = 'reject-template-audit' THEN RAISE EXCEPTION 'forced audit insertion failure'; END IF; RETURN NEW; END $$;
	CREATE TRIGGER reject_template_audit BEFORE INSERT ON write_audit_operations FOR EACH ROW EXECUTE FUNCTION reject_template_audit();
	CREATE TRIGGER reject_template_admin_audit BEFORE INSERT ON admin_audit_log FOR EACH ROW EXECUTE FUNCTION reject_template_audit()`)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func (f fixture) snapshot(t *testing.T) map[string]string {
	t.Helper()
	result := make(map[string]string)
	for _, table := range []string{"environment_templates", "write_audit_operations", "write_audit_owners", "admin_audit_log"} {
		var rows string
		if err := f.pool.QueryRow(t.Context(), "SELECT COALESCE(jsonb_agg(to_jsonb(r) ORDER BY to_jsonb(r)::text)::text, '[]') FROM "+pgx.Identifier{table}.Sanitize()+" r").Scan(&rows); err != nil {
			t.Fatalf("snapshot %s: %v", table, err)
		}
		result[table] = rows
	}
	return result
}

func writeSource(ctx context.Context, tenant, request string) context.Context {
	return writeaudit.WithSource(ctx, writeaudit.Source{
		KeyID: "static:" + strings.Repeat("a", 64), Name: "template audit fixture", Prefix: "aaaaaaaa",
		Kind: "static", TenantID: tenant, RequestID: request, TraceID: "template-audit-trace",
	})
}

// Each write records its audit operation in its own transaction: a failed
// audit insert leaves no Template change, and a successful write records the
// action, the Template ID and, for create, the new owner.
func TestWriteAuditCommitsWithTheTemplate(t *testing.T) {
	f := auditFixture(t)
	for _, action := range []string{"create", "update", "delete"} {
		t.Run(action, func(t *testing.T) {
			tenant := uuid.NewString()
			existing := ""
			if action != "create" {
				existing = f.create(t, tenant, environmenttemplates.Input{}).ID
			}
			run := func(ctx context.Context) (string, error) {
				switch action {
				case "create":
					v, err := f.service.Create(ctx, environmenttemplates.CreateCommand{TenantID: tenant})
					return v.ID, err
				case "update":
					v, err := f.service.Update(ctx, environmenttemplates.UpdateCommand{TenantID: tenant, TemplateID: existing, Input: environmenttemplates.Input{SetName: true, Name: ptr("replacement")}})
					return v.ID, err
				}
				return f.service.Delete(ctx, environmenttemplates.DeleteCommand{TenantID: tenant, TemplateID: existing})
			}
			before := f.snapshot(t)
			if _, err := run(writeSource(t.Context(), tenant, rejectedRequest)); err == nil {
				t.Fatal("audit failure was accepted")
			}
			if !reflect.DeepEqual(before, f.snapshot(t)) {
				t.Fatal("audit failure left Template or audit changes")
			}
			request := uuid.NewString()
			id, err := run(writeSource(t.Context(), tenant, request))
			if err != nil {
				t.Fatal(err)
			}
			var gotAction, kind, gotID string
			var parent *string
			if err := f.pool.QueryRow(t.Context(), `SELECT action,resource_type,resource_id,parent_id FROM write_audit_operations WHERE tenant_id=$1 AND request_id=$2`, tenant, request).Scan(&gotAction, &kind, &gotID, &parent); err != nil {
				t.Fatal(err)
			}
			if gotAction != action || kind != "environment_template" || gotID != id || (parent != nil && *parent != "") {
				t.Fatalf("wrong operation identity: %s %s %s", gotAction, kind, gotID)
			}
			owners, want := 0, 0
			if action == "create" {
				want = 1
			}
			if err := f.pool.QueryRow(t.Context(), `SELECT count(*) FROM write_audit_owners WHERE tenant_id=$1 AND resource_type='environment_template' AND resource_id=$2`, tenant, id).Scan(&owners); err != nil || owners != want {
				t.Fatalf("ownership count %d, want %d: %v", owners, want, err)
			}
		})
	}
}

func TestInvalidWriteAuditSourcePassesThrough(t *testing.T) {
	f := newFixture(t, pgtest.Open(t))
	tenant := uuid.NewString()
	_, err := f.service.Create(writeSource(t.Context(), uuid.NewString(), uuid.NewString()), environmenttemplates.CreateCommand{TenantID: tenant})
	if !errors.Is(err, writeaudit.ErrInvalidSource) {
		t.Fatal("mismatched source tenant", err)
	}
	if page, err := f.keyless.List(t.Context(), tenant, environmenttemplates.ListQuery{Limit: 1}); err != nil || len(page.Templates) != 0 {
		t.Fatal("rejected source left a Template", err)
	}
}

// An administrator deletion records only an administrator audit row, even
// under an inherited public-key source, and a failed audit restores the
// Template with its sealed configuration.
func TestAdminDeleteAuditCommitsWithTheDeletion(t *testing.T) {
	f := auditFixture(t)
	tenant := uuid.NewString()
	template := f.create(t, tenant, environmenttemplates.Input{
		SetEnv: true, SetCommands: true, SetFiles: true,
		Setup: environmentconfig.Setup{Env: map[string]string{"PRIVATE": "admin-private-env"}, Commands: []environmentconfig.SetupCommand{{Command: "printf admin-private-env"}}},
		Files: []environmentconfig.InitialFile{{Type: "inline", Path: "/workspace/private", Data: []byte("admin-private-body")}},
	})
	// Administrator audit rows name the Project that owns the tenant.
	if _, err := f.pool.Exec(t.Context(), "INSERT INTO execution_project_scopes(tenant_id,organization_id,project_id) VALUES($1,'admin-delete',$2)", tenant, tenant); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), "INSERT INTO projects(id,name,tenant_id,subject_kind,subject_id) VALUES($1,'Delete fixture',$1,'service_account',$2)", tenant, "project:"+tenant); err != nil {
		t.Fatal(err)
	}
	admin := func(request string) context.Context {
		return adminaudit.WithSource(writeSource(t.Context(), tenant, request), adminaudit.Source{
			CredentialID: "87654321", ActorLabel: "administrator fixture", ProjectID: tenant, RequestID: request, TraceID: "admin-mutation-trace",
		})
	}
	before := f.snapshot(t)
	if _, err := f.service.Delete(admin(rejectedRequest), environmenttemplates.DeleteCommand{TenantID: tenant, TemplateID: template.ID}); err == nil {
		t.Fatal("audit failure was accepted")
	}
	if !reflect.DeepEqual(before, f.snapshot(t)) {
		t.Fatal("failed administrator audit changed the Template or audit rows")
	}
	if restored := f.resolve(t, tenant, template.ID); restored.Setup.Env["PRIVATE"] != "admin-private-env" || len(restored.Files) != 1 {
		t.Fatal("failed deletion lost sealed configuration")
	}
	request := uuid.NewString()
	id, err := f.service.Delete(admin(request), environmenttemplates.DeleteCommand{TenantID: tenant, TemplateID: template.ID})
	if err != nil || id != template.ID {
		t.Fatal(id, err)
	}
	var credential, actor, project, trace, action, kind, gotID, mappings, raw string
	if err := f.pool.QueryRow(t.Context(), `SELECT admin_credential_id,actor_label,project_id,trace_id,action,resource_type,resource_id,result_ids::text,to_jsonb(a)::text FROM admin_audit_log a WHERE tenant_id=$1 AND request_id=$2`, tenant, request).Scan(&credential, &actor, &project, &trace, &action, &kind, &gotID, &mappings, &raw); err != nil {
		t.Fatal(err)
	}
	if credential != "87654321" || actor != "administrator fixture" || project != tenant || trace != "admin-mutation-trace" || action != "delete" || kind != "environment_template" || gotID != id || mappings != "[]" || strings.Contains(raw, "admin-private") {
		t.Fatal("administrator audit identity differs")
	}
	var operations, owners int
	if err := f.pool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM write_audit_operations WHERE tenant_id=$1 AND action='delete'),(SELECT count(*) FROM write_audit_owners WHERE tenant_id=$1 AND resource_type='environment_template' AND resource_id=$2)`, tenant, id).Scan(&operations, &owners); err != nil || operations != 0 || owners != 0 {
		t.Fatal("administrator impersonated public-key provenance", err)
	}
	if _, err := f.keyless.Get(t.Context(), tenant, id); !errors.Is(err, environmenttemplates.ErrNotFound) {
		t.Fatal("deleted Template is visible", err)
	}
}
