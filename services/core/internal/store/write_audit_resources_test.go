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
		"skill_create", "skill_upload_version", "skill_update_default", "skill_delete", "version_delete", "version_delete_last",
	} {
		t.Run(name, func(t *testing.T) {
			tenant := uuid.NewString()
			mutation := prepareResourceAuditMutation(t, s, tenant, name, archive)
			snapshot := func() map[string]string {
				result := make(map[string]string)
				for _, table := range []string{"agents", "skills", "skill_versions", "write_audit_operations", "write_audit_owners"} {
					query := "SELECT COALESCE(jsonb_agg(to_jsonb(r) ORDER BY to_jsonb(r)::text)::text, '[]') FROM " + pgx.Identifier{table}.Sanitize() + " r WHERE tenant_id=$1"
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
	t.Fatal("unknown resource audit mutation", name)
	return resourceAuditMutation{}
}
