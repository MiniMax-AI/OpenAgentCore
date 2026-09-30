package store

import (
	"bytes"
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/adminaudit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/writeaudit"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const rejectedAdminRequest = "reject-admin-mutation-fixture"

func rejectAdminAuditInsert(t *testing.T, s *Store) {
	t.Helper()
	_, err := s.pool.Exec(t.Context(), `CREATE SEQUENCE admin_mutation_rejections;
 CREATE FUNCTION reject_admin_mutation_fixture() RETURNS trigger LANGUAGE plpgsql AS $$
 BEGIN IF NEW.request_id = 'reject-admin-mutation-fixture' THEN PERFORM nextval('admin_mutation_rejections'); RAISE EXCEPTION 'forced administrator audit failure'; END IF; RETURN NEW; END $$;
 CREATE TRIGGER reject_admin_mutation_fixture BEFORE INSERT ON admin_audit_log FOR EACH ROW EXECUTE FUNCTION reject_admin_mutation_fixture()`)
	if err != nil {
		t.Fatal(err)
	}
}

// Sequence increments survive rollback, so this test-only counter proves the
// audit trigger ran even when credential stores sanitize the database error.
func adminAuditRejections(t *testing.T, s *Store) int64 {
	t.Helper()
	var count int64
	if err := s.pool.QueryRow(t.Context(), "SELECT CASE WHEN is_called THEN last_value ELSE 0 END FROM admin_mutation_rejections").Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func requireAdminAuditFailure(t *testing.T, s *Store, err error, before int64) {
	t.Helper()
	if err == nil || adminAuditRejections(t, s) != before+1 {
		t.Fatal("mutation did not reach the failing final audit insertion", err)
	}
}

type resourceAuditMutation struct {
	action, kind, parent string
	run                  func(context.Context) (string, error)
}

func adminDeleteContext(ctx context.Context, tenant, request string) context.Context {
	// Even an inherited public provenance context must not turn an administrator
	// operation into a user-key operation.
	public := writeaudit.WithSource(ctx, writeaudit.Source{
		KeyID: "static:" + strings.Repeat("a", 64), Name: "resource audit fixture", Prefix: "aaaaaaaa",
		Kind: "static", TenantID: tenant, RequestID: request, TraceID: "resource-audit-trace",
	})
	return adminaudit.WithSource(public, adminaudit.Source{
		CredentialID: "87654321", ActorLabel: "administrator fixture", ProjectID: tenant, RequestID: request, TraceID: "admin-mutation-trace",
	})
}

// These tests own an isolated database. Complete table snapshots include child
// ciphertext, counters and timestamps; LO pages prove byte-for-byte rollback.
func adminMutationSnapshot(t *testing.T, s *Store, tables ...string) map[string]string {
	t.Helper()
	result := make(map[string]string, len(tables))
	for _, table := range tables {
		var rows string
		if err := s.pool.QueryRow(t.Context(), "SELECT COALESCE(jsonb_agg(to_jsonb(r) ORDER BY to_jsonb(r)::text)::text,'[]') FROM "+pgx.Identifier{table}.Sanitize()+" r").Scan(&rows); err != nil {
			t.Fatalf("snapshot %s: %v", table, err)
		}
		result[table] = rows
	}
	return result
}

func assertAdminMutationAudit(t *testing.T, s *Store, tenant, request, action, kind, id string) {
	t.Helper()
	var credential, actor, key, trace, gotAction, gotKind, gotID, mappings, raw string
	if err := s.pool.QueryRow(t.Context(), `SELECT admin_credential_id,actor_label,project_id,trace_id,action,resource_type,resource_id,result_ids::text,to_jsonb(a)::text
 FROM admin_audit_log a WHERE tenant_id=$1 AND request_id=$2`, tenant, request).Scan(&credential, &actor, &key, &trace, &gotAction, &gotKind, &gotID, &mappings, &raw); err != nil {
		t.Fatal(err)
	}
	expectedKey := tenant
	if credential != "87654321" || actor != "administrator fixture" || key != expectedKey || trace != "admin-mutation-trace" || gotAction != action || gotKind != kind || gotID != id || mappings != "[]" {
		t.Fatal("administrator audit identity differs")
	}
	for _, secret := range []string{"admin-private-body", "private-agent-canary", "audit-private-token"} {
		if strings.Contains(raw, secret) {
			t.Fatal("private content entered administrator audit")
		}
	}
	var audits, operations, owners int
	if err := s.pool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM admin_audit_log WHERE tenant_id=$1 AND request_id=$2),(SELECT count(*) FROM write_audit_operations WHERE tenant_id=$1),(SELECT count(*) FROM write_audit_owners WHERE tenant_id=$1)`, tenant, request).Scan(&audits, &operations, &owners); err != nil {
		t.Fatal(err)
	}
	if audits != 1 || operations != 0 || owners != 0 {
		t.Fatal("administrator impersonated public-key provenance")
	}
}

func TestAdminDeleteResourceAuditTransactions(t *testing.T) {
	_, pool := newManagedTestStore(t)
	cipher, err := credentialcrypto.New(bytes.Repeat([]byte{94}, 32))
	if err != nil {
		t.Fatal(err)
	}
	s := NewWithCredentialCipher(pool, cipher)
	rejectAdminAuditInsert(t, s)
	tables := []string{"agents", "agent_model_execution", "sessions", "turns", "environments", "session_artifacts", "admin_audit_log", "write_audit_operations", "write_audit_owners", "pg_largeobject_metadata", "pg_largeobject"}
	for _, name := range []string{"session_delete", "artifact_delete"} {
		t.Run(name, func(t *testing.T) {
			tenant, mutation, verifyRestored, removedObjects := prepareAdminHistoryDelete(t, s, name)
			if _, err := pool.Exec(t.Context(), "INSERT INTO execution_project_scopes(tenant_id,organization_id,project_id) VALUES($1,'admin-delete',$2)", tenant, tenant); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(t.Context(), "INSERT INTO projects(id,name,tenant_id,subject_kind,subject_id) VALUES($1,'Delete fixture',$1,'service_account',$2)", tenant, "project:"+tenant); err != nil {
				t.Fatal(err)
			}
			before, objects := adminMutationSnapshot(t, s, tables...), largeObjectCount(t, pool)
			rejections := adminAuditRejections(t, s)
			_, err := mutation.run(adminDeleteContext(t.Context(), tenant, rejectedAdminRequest))
			requireAdminAuditFailure(t, s, err, rejections)
			if !reflect.DeepEqual(before, adminMutationSnapshot(t, s, tables...)) {
				t.Fatal("failed administrator audit changed resources, cascades, audit rows or LO bytes")
			}
			if verifyRestored != nil {
				verifyRestored()
			}
			request := uuid.NewString()
			id, err := mutation.run(adminDeleteContext(t.Context(), tenant, request))
			if err != nil {
				t.Fatal(err)
			}
			assertAdminMutationAudit(t, s, tenant, request, "delete", mutation.kind, id)
			if largeObjectCount(t, pool) != objects-removedObjects {
				t.Fatal("successful deletion did not unlink exactly its large objects")
			}
			assertAdminDeletedResource(t, s, tenant, mutation, id)
		})
	}
}

func prepareAdminHistoryDelete(t *testing.T, s *Store, name string) (string, resourceAuditMutation, func(), int) {
	t.Helper()
	tenant, session, environment, turn := artifactTurn(t, s, "self_hosted")
	body := bytes.Repeat([]byte("admin-private-body"), 500)
	archive := artifactArchive(t, map[string][]byte{"outputs/private.txt": body, "outputs/other.txt": []byte("second body")})
	if err := s.StageTurnArtifacts(t.Context(), tenant, session, turn, environment, bytes.NewReader(archive)); err != nil {
		t.Fatal(err)
	}
	transition(t, s, tenant, session, turn, sessions.TurnInProgress, sessions.TurnCompleted)
	page, err := s.ListSessionArtifacts(t.Context(), tenant, session, "", "", 100, true)
	if err != nil || len(page.Artifacts) != 2 {
		t.Fatal("artifact fixture", err)
	}
	verify := func() {
		if _, err := s.GetSession(t.Context(), tenant, session); err != nil {
			t.Fatal("Session was not restored", err)
		}
		for _, artifact := range page.Artifacts {
			want := body
			if artifact.Path == "/workspace/outputs/other.txt" {
				want = []byte("second body")
			}
			if err := s.ReadSessionArtifact(t.Context(), tenant, session, artifact.ID, func(_ sessions.Artifact, r io.Reader) error {
				got, err := io.ReadAll(r)
				if !bytes.Equal(got, want) {
					t.Error("artifact large-object bytes were not restored")
				}
				return err
			}); err != nil {
				t.Fatal(err)
			}
		}
	}
	if name == "session_delete" {
		return tenant, resourceAuditMutation{action: "delete", kind: "session", run: func(ctx context.Context) (string, error) { return session, s.DeleteSession(ctx, tenant, session) }}, verify, 2
	}
	artifact := page.Artifacts[0]
	return tenant, resourceAuditMutation{action: "delete", kind: "artifact", parent: session, run: func(ctx context.Context) (string, error) {
		return artifact.ID, s.DeleteSessionArtifact(ctx, tenant, session, artifact.ID)
	}}, verify, 1
}

func assertAdminDeletedResource(t *testing.T, s *Store, tenant string, mutation resourceAuditMutation, id string) {
	t.Helper()
	var err error
	switch mutation.kind {
	case "session":
		_, err = s.GetSession(t.Context(), tenant, id)
	case "artifact":
		_, err = s.GetSessionArtifact(t.Context(), tenant, mutation.parent, id)
	default:
		t.Fatal("unsupported delete fixture")
	}
	if !errors.Is(err, sessions.ErrNotFound) {
		t.Fatal("successful deletion left resource visible", err)
	}
}
