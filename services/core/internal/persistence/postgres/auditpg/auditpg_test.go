package auditpg_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/adminaudit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/auditpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgtest"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/writeaudit"
)

func openAudit(t *testing.T) (*pgunit.Pool, *auditpg.Store) {
	t.Helper()
	pool := pgunit.NewPool(pgtest.Open(t))
	return pool, auditpg.New(pool)
}

func uuidOf(id string) pgtype.UUID { return pgtype.UUID{Bytes: uuid.MustParse(id), Valid: true} }

func staticSource(tenant string) writeaudit.Source {
	sum := sha256.Sum256([]byte(uuid.NewString()))
	digest := hex.EncodeToString(sum[:])
	return writeaudit.Source{KeyID: "static:" + digest, Prefix: digest[:8], Name: "test key", Kind: "static", TenantID: tenant, RequestID: uuid.NewString(), TraceID: uuid.NewString()}
}

func adminSource(projectID string) adminaudit.Source {
	return adminaudit.Source{CredentialID: "12345678", ActorLabel: "test", RequestID: uuid.NewString(), TraceID: uuid.NewString(), ProjectID: projectID}
}

// record runs one audited write in its own transaction.
func record(t *testing.T, pool *pgunit.Pool, ctx context.Context, write func(context.Context, *sqlc.Queries) error) error {
	t.Helper()
	return pool.Transaction(ctx, func(ctx context.Context, tx pgx.Tx) error { return write(ctx, sqlc.New(tx)) })
}

func recordWrite(t *testing.T, pool *pgunit.Pool, source writeaudit.Source, action, kind, id string, created ...writeaudit.Resource) {
	t.Helper()
	if err := record(t, pool, writeaudit.WithSource(t.Context(), source), func(ctx context.Context, q *sqlc.Queries) error {
		return auditpg.RecordWriteAudit(ctx, q, source.TenantID, action, kind, id, "", created...)
	}); err != nil {
		t.Fatal(err)
	}
}

type project struct{ id, tenant string }

func createProject(t *testing.T, pool *pgunit.Pool) project {
	t.Helper()
	p := project{id: uuid.NewString(), tenant: uuid.NewString()}
	if err := record(t, pool, t.Context(), func(ctx context.Context, q *sqlc.Queries) error {
		if _, err := q.EnsureProjectScope(ctx, sqlc.EnsureProjectScopeParams{TenantID: uuidOf(p.tenant), OrganizationID: "org_" + p.id, ProjectID: "proj_" + p.id}); err != nil {
			return err
		}
		_, err := q.CreateProject(ctx, sqlc.CreateProjectParams{ID: uuidOf(p.id), Name: "Project", TenantID: uuidOf(p.tenant), SubjectID: p.id})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return p
}

func createAgent(ctx context.Context, q *sqlc.Queries, tenant, id string) error {
	_, err := q.CreateAgent(ctx, sqlc.CreateAgentParams{ID: uuidOf(id), TenantID: uuidOf(tenant), Metadata: []byte("{}"), Configuration: []byte("{}")})
	return err
}

func agentExists(t *testing.T, pool *pgunit.Pool, tenant, id string) bool {
	t.Helper()
	var exists bool
	if err := pool.Snapshot(t.Context(), func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM agents WHERE tenant_id=$1 AND id=$2)", tenant, id).Scan(&exists)
	}); err != nil {
		t.Fatal(err)
	}
	return exists
}

func exec(t *testing.T, pool *pgunit.Pool, sql string, args ...any) {
	t.Helper()
	if err := pool.Transaction(t.Context(), func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, sql, args...)
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

// Audit rows commit and roll back with the business write that records them.
func TestWriteAuditCommitsAndRollsBackWithTheBusinessWrite(t *testing.T) {
	pool, audit := openAudit(t)
	tenant := uuid.NewString()
	source := staticSource(tenant)
	for _, failure := range []string{"", "after_audit", "invalid_source", "database_audit_failure"} {
		t.Run(failure, func(t *testing.T) {
			id := uuid.NewString()
			source.RequestID = uuid.NewString()
			if failure == "invalid_source" {
				source.TraceID = ""
			} else {
				source.TraceID = uuid.NewString()
			}
			err := pool.Transaction(writeaudit.WithSource(t.Context(), source), func(ctx context.Context, tx pgx.Tx) error {
				q := sqlc.New(tx)
				if err := createAgent(ctx, q, tenant, id); err != nil {
					return err
				}
				if failure == "database_audit_failure" {
					// This transaction-local constraint deliberately rejects the audit insert.
					if _, err := tx.Exec(ctx, "ALTER TABLE write_audit_operations ADD CONSTRAINT audit_test_failure CHECK (false) NOT VALID"); err != nil {
						return err
					}
				}
				if err := auditpg.RecordWriteAudit(ctx, q, tenant, "create", "agent", id, "", writeaudit.Resource{Type: "agent", ID: id}); err != nil {
					return err
				}
				if failure == "after_audit" {
					return errors.New("business failure after audit")
				}
				return nil
			})
			if (err != nil) != (failure != "") || failure == "invalid_source" && !errors.Is(err, writeaudit.ErrInvalidSource) {
				t.Fatalf("commit result: %v", err)
			}
			if agentExists(t, pool, tenant, id) != (failure == "") {
				t.Fatal("business write did not follow the audit outcome")
			}
			page, err := audit.ListWriteOperations(t.Context(), tenant, writeaudit.Filter{ResourceID: id})
			want := 0
			if failure == "" {
				want = 1
			}
			if err != nil || len(page.Data) != want {
				t.Fatalf("audit count %d want %d: %v", len(page.Data), want, err)
			}
			owners, err := audit.GetResourceOwners(t.Context(), tenant, "agent", []string{id})
			if err != nil || (owners[0].APIKey != nil) != (failure == "") {
				t.Fatalf("ownership rollback: %+v %v", owners, err)
			}
		})
	}
}

// A write without provenance is intentional internal work: no row, no error.
func TestWriteWithoutProvenanceStaysUnattributed(t *testing.T) {
	pool, audit := openAudit(t)
	tenant, id := uuid.NewString(), uuid.NewString()
	if err := record(t, pool, t.Context(), func(ctx context.Context, q *sqlc.Queries) error {
		if err := createAgent(ctx, q, tenant, id); err != nil {
			return err
		}
		return auditpg.RecordWriteAudit(ctx, q, tenant, "create", "agent", id, "", writeaudit.Resource{Type: "agent", ID: id})
	}); err != nil {
		t.Fatal(err)
	}
	page, err := audit.ListWriteOperations(t.Context(), tenant, writeaudit.Filter{})
	if err != nil || len(page.Data) != 0 || !agentExists(t, pool, tenant, id) {
		t.Fatalf("unattributed write: %+v %v", page, err)
	}
	owners, err := audit.GetResourceOwners(t.Context(), tenant, "agent", []string{id})
	if err != nil || owners[0].APIKey != nil || owners[0].Source != nil {
		t.Fatalf("unattributed owner: %+v %v", owners, err)
	}
}

// Malformed provenance fails closed before any statement runs, so a nil q
// shows that nothing reached the database.
func TestMalformedProvenanceFailsClosed(t *testing.T) {
	valid := staticSource(uuid.NewString())
	for _, field := range []string{"tenant", "key", "prefix", "kind", "request", "trace", "name", "action", "resource_type", "created_type", "resource_id"} {
		source, action, kind, id := valid, "create", "agent", "resource"
		created := []writeaudit.Resource{{Type: "agent", ID: "resource"}}
		switch field {
		case "tenant":
			source.TenantID = uuid.NewString()
		case "key":
			source.KeyID = "static:abcd"
		case "prefix":
			source.Prefix = "bad"
		case "kind":
			source.Kind = "unknown"
		case "request":
			source.RequestID = ""
		case "trace":
			source.TraceID = ""
		case "name":
			source.Name = strings.Repeat("x", 81)
		case "action":
			action = "copy"
		case "resource_type":
			kind = "project"
		case "created_type":
			created[0].Type = "project"
		case "resource_id":
			id = ""
		}
		ctx := writeaudit.WithSource(t.Context(), source)
		if err := auditpg.RecordWriteAudit(ctx, nil, valid.TenantID, action, kind, id, "", created...); !errors.Is(err, writeaudit.ErrInvalidSource) {
			t.Fatalf("%s accepted: %v", field, err)
		}
	}
	admin := adminSource(uuid.NewString())
	for name, ctx := range map[string]context.Context{
		"missing":         t.Context(),
		"no request":      adminaudit.WithSource(t.Context(), adminaudit.Source{CredentialID: admin.CredentialID, TraceID: admin.TraceID, ProjectID: admin.ProjectID}),
		"no credential":   adminaudit.WithSource(t.Context(), adminaudit.Source{RequestID: admin.RequestID, TraceID: admin.TraceID, ProjectID: admin.ProjectID}),
		"no project":      adminaudit.WithSource(t.Context(), adminaudit.Source{CredentialID: admin.CredentialID, RequestID: admin.RequestID, TraceID: admin.TraceID}),
		"invalid project": adminaudit.WithSource(t.Context(), adminaudit.Source{CredentialID: admin.CredentialID, RequestID: admin.RequestID, TraceID: admin.TraceID, ProjectID: "project"}),
	} {
		if err := auditpg.RecordAdminMutation(ctx, nil, uuid.NewString(), "update", "agent", "resource"); !errors.Is(err, adminaudit.ErrInvalidSource) {
			t.Fatalf("administrator source %s accepted: %v", name, err)
		}
	}
	for name, ctx := range map[string]context.Context{
		"missing":       t.Context(),
		"project scope": adminaudit.WithSource(t.Context(), admin),
	} {
		if err := auditpg.RecordDeploymentMutation(ctx, nil, "set", "deployment_model_provider", "codex"); !errors.Is(err, adminaudit.ErrInvalidSource) {
			t.Fatalf("deployment source %s accepted: %v", name, err)
		}
	}
}

// Administrator provenance takes precedence over a write source in the same
// context, and deployment mutations record no Project.
func TestAdministratorProvenance(t *testing.T) {
	pool, audit := openAudit(t)
	p := createProject(t, pool)
	id := uuid.NewString()
	ctx := adminaudit.WithSource(writeaudit.WithSource(t.Context(), staticSource(p.tenant)), adminSource(p.id))
	if err := record(t, pool, ctx, func(ctx context.Context, q *sqlc.Queries) error {
		if err := createAgent(ctx, q, p.tenant, id); err != nil {
			return err
		}
		return auditpg.RecordWriteAudit(ctx, q, p.tenant, "create", "agent", id, "", writeaudit.Resource{Type: "agent", ID: id})
	}); err != nil {
		t.Fatal(err)
	}
	writes, err := audit.ListWriteOperations(t.Context(), p.tenant, writeaudit.Filter{})
	if err != nil || len(writes.Data) != 0 {
		t.Fatalf("write source recorded beside administrator provenance: %+v %v", writes, err)
	}
	page, err := audit.ListAdminAudit(t.Context(), adminaudit.Filter{ProjectID: p.id})
	if err != nil || len(page.Data) != 1 || page.Data[0].Action != "create" || page.Data[0].ResourceID != id || page.Data[0].ProjectID == nil || *page.Data[0].ProjectID != p.id {
		t.Fatalf("administrator audit: %+v %v", page, err)
	}
	resource := "deployment-" + uuid.NewString()
	if err := record(t, pool, adminaudit.WithSource(t.Context(), adminSource("")), func(ctx context.Context, q *sqlc.Queries) error {
		return auditpg.RecordDeploymentMutation(ctx, q, "set", "deployment_model_provider", resource)
	}); err != nil {
		t.Fatal(err)
	}
	page, err = audit.ListAdminAudit(t.Context(), adminaudit.Filter{ResourceID: resource})
	if err != nil || len(page.Data) != 1 || page.Data[0].ProjectID != nil || page.Data[0].Action != "set" {
		t.Fatalf("deployment audit: %+v %v", page, err)
	}
	// A failed business write leaves no administrator row.
	failed := errors.New("business failure after audit")
	if err := record(t, pool, adminaudit.WithSource(t.Context(), adminSource(p.id)), func(ctx context.Context, q *sqlc.Queries) error {
		if err := auditpg.RecordAdminMutation(ctx, q, p.tenant, "delete", "agent", id); err != nil {
			return err
		}
		return failed
	}); !errors.Is(err, failed) {
		t.Fatal(err)
	}
	if page, err := audit.ListAdminAudit(t.Context(), adminaudit.Filter{ProjectID: p.id, Action: "delete"}); err != nil || len(page.Data) != 0 {
		t.Fatalf("rolled back administrator audit: %+v %v", page, err)
	}
}

func TestWriteAuditOwnersIdentityReplayAndRevocation(t *testing.T) {
	pool, audit := openAudit(t)
	tenant := uuid.NewString()
	a := staticSource(tenant)
	id, implicit := uuid.NewString(), uuid.NewString()
	recordWrite(t, pool, a, "create", "session", id, writeaudit.Resource{Type: "session", ID: id}, writeaudit.Resource{Type: "environment", ID: implicit, ParentID: id})
	// Same request may reach a commit receipt twice but cannot create another owner.
	replayID := uuid.NewString()
	recordWrite(t, pool, a, "create", "session", id, writeaudit.Resource{Type: "session", ID: replayID})
	b := staticSource(tenant)
	b.Kind = "console"
	recordWrite(t, pool, b, "update", "session", id)
	owners, err := audit.GetResourceOwners(t.Context(), tenant, "session", []string{replayID, id, id, "historical"})
	if err != nil || len(owners) != 4 || owners[0].APIKey != nil || owners[1].APIKey.ID != a.KeyID || owners[2].APIKey.ID != a.KeyID || owners[3].APIKey != nil {
		t.Fatalf("owners %+v: %v", owners, err)
	}
	implicitOwners, err := audit.GetResourceOwners(t.Context(), tenant, "environment", []string{implicit})
	if err != nil || implicitOwners[0].APIKey.ID != a.KeyID {
		t.Fatalf("implicit owner: %+v %v", implicitOwners, err)
	}
	foreign, err := audit.GetResourceOwners(t.Context(), uuid.NewString(), "session", []string{id})
	if err != nil || foreign[0].APIKey != nil {
		t.Fatalf("foreign owner: %+v %v", foreign, err)
	}
	page, err := audit.ListWriteOperations(t.Context(), tenant, writeaudit.Filter{ResourceID: id})
	if err != nil || len(page.Data) != 2 || page.Data[0].APIKey.Kind != "console" || page.Data[1].APIKey.ID != a.KeyID {
		t.Fatalf("request dedup or key identity: %+v %v", page, err)
	}
	p := createProject(t, pool)
	keyID := uuid.NewString()
	sum := sha256.Sum256([]byte(keyID))
	if err := record(t, pool, t.Context(), func(ctx context.Context, q *sqlc.Queries) error {
		_, err := q.CreateProjectAPIKey(ctx, sqlc.CreateProjectAPIKeyParams{ID: uuidOf(keyID), Name: "issued key", Prefix: "pc_" + hex.EncodeToString(sum[:4]), TokenSha256: hex.EncodeToString(sum[:]), ProjectID: uuidOf(p.id)})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	c := staticSource(p.tenant)
	c.KeyID, c.Name, c.Prefix, c.Kind = keyID, "issued key", "pc_"+hex.EncodeToString(sum[:4]), "issued"
	fileID := "file_" + uuid.NewString()
	recordWrite(t, pool, c, "create", "file", fileID, writeaudit.Resource{Type: "file", ID: fileID})
	if err := record(t, pool, t.Context(), func(ctx context.Context, q *sqlc.Queries) error {
		_, err := q.RevokeProjectAPIKey(ctx, sqlc.RevokeProjectAPIKeyParams{ID: uuidOf(keyID), ProjectID: uuidOf(p.id)})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	revoked, err := audit.GetResourceOwners(t.Context(), p.tenant, "file", []string{fileID})
	if err != nil || revoked[0].APIKey.RevokedAt == nil || revoked[0].APIKey.Name != "issued key" {
		t.Fatalf("revocation metadata: %+v %v", revoked, err)
	}
	page, err = audit.ListWriteOperations(t.Context(), p.tenant, writeaudit.Filter{KeyID: c.KeyID})
	if err != nil || len(page.Data) != 1 || page.Data[0].APIKey.RevokedAt == nil {
		t.Fatalf("history revocation: %+v %v", page, err)
	}
}

// The copy operation was removed; its committed provenance must stay readable.
func TestHistoricalAdminCopyProvenance(t *testing.T) {
	pool, audit := openAudit(t)
	p := createProject(t, pool)
	auditID, agentID := uuid.NewString(), uuid.NewString()
	exec(t, pool, `INSERT INTO admin_audit_log(id,tenant_id,project_id,admin_credential_id,actor_label,action,resource_type,resource_id,result_ids,request_id,trace_id)
		VALUES($1,$2,$3,'digest','admin','copy','agent','source-agent',$4::jsonb,'request','trace')`, auditID, p.tenant, p.id, `[{"type":"agent","source_id":"source-agent","target_id":"`+agentID+`"}]`)
	exec(t, pool, "INSERT INTO admin_resource_owners(tenant_id,resource_type,resource_id,audit_id) VALUES($1,'agent',$2,$3)", p.tenant, agentID, auditID)
	owners, err := audit.GetResourceOwners(t.Context(), p.tenant, "agent", []string{agentID})
	if err != nil || len(owners) != 1 || owners[0].APIKey != nil || owners[0].Source == nil || *owners[0].Source != "admin_copy" || owners[0].AdminAuditID == nil || *owners[0].AdminAuditID != auditID {
		t.Fatalf("historical copy owner: %+v %v", owners, err)
	}
	page, err := audit.ListAdminAudit(t.Context(), adminaudit.Filter{ProjectID: p.id, Action: "copy"})
	if err != nil || len(page.Data) != 1 || page.Data[0].ID != auditID || !strings.Contains(string(page.Data[0].ResultIDs), agentID) {
		t.Fatalf("historical copy audit: %+v %v", page, err)
	}
}

func TestWriteAuditCursorFiltersAndRetention(t *testing.T) {
	pool, audit := openAudit(t)
	tenant, id := uuid.NewString(), uuid.NewString()
	source := staticSource(tenant)
	if err := record(t, pool, t.Context(), func(ctx context.Context, q *sqlc.Queries) error { return createAgent(ctx, q, tenant, id) }); err != nil {
		t.Fatal(err)
	}
	recordWrite(t, pool, source, "create", "agent", id, writeaudit.Resource{Type: "agent", ID: id})
	for i := 0; i < 4; i++ {
		source.RequestID = uuid.NewString()
		recordWrite(t, pool, source, "update", "agent", id)
	}
	// Equal timestamps exercise the ID tie breaker, independent of insertion order.
	stamp := time.Date(1800, 1, 1, 0, 0, 0, 0, time.UTC)
	exec(t, pool, "UPDATE write_audit_operations SET created_at=$1 WHERE tenant_id=$2", stamp, tenant)
	first, err := audit.ListWriteOperations(t.Context(), tenant, writeaudit.Filter{Limit: 2})
	if err != nil || len(first.Data) != 2 || !first.HasMore || first.NextCursor == "" {
		t.Fatalf("first %+v %v", first, err)
	}
	seen := map[string]bool{}
	page := first
	for {
		for _, row := range page.Data {
			if seen[row.ID] {
				t.Fatal("duplicate cursor item")
			}
			seen[row.ID] = true
		}
		if !page.HasMore {
			break
		}
		page, err = audit.ListWriteOperations(t.Context(), tenant, writeaudit.Filter{Limit: 2, After: page.NextCursor})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(seen) != 5 {
		t.Fatalf("lost rows %d", len(seen))
	}
	for _, filter := range []writeaudit.Filter{{Limit: 2, After: first.NextCursor, KeyID: "different"}, {After: "malformed"}, {Limit: 101}, {ResourceType: "project"}} {
		if _, err := audit.ListWriteOperations(t.Context(), tenant, filter); !errors.Is(err, writeaudit.ErrInvalidQuery) {
			t.Fatalf("filter %+v: %v", filter, err)
		}
	}
	if _, err := audit.ListWriteOperations(t.Context(), uuid.NewString(), writeaudit.Filter{After: first.NextCursor}); !errors.Is(err, writeaudit.ErrInvalidQuery) {
		t.Fatalf("cross tenant cursor: %v", err)
	}
	for _, filter := range []writeaudit.Filter{{CreatedBefore: &stamp}, {KeyID: "missing"}, {ResourceType: "file"}, {ResourceID: "missing"}} {
		page, err := audit.ListWriteOperations(t.Context(), tenant, filter)
		if err != nil || len(page.Data) != 0 {
			t.Fatalf("filter %+v: %+v %v", filter, page, err)
		}
	}
	inclusive, err := audit.ListWriteOperations(t.Context(), tenant, writeaudit.Filter{CreatedAfter: &stamp})
	if err != nil || len(inclusive.Data) != 5 {
		t.Fatalf("inclusive lower bound: %+v %v", inclusive, err)
	}
	cutoff := stamp.Add(time.Hour)
	n, err := audit.DeleteExpiredWriteOperations(t.Context(), cutoff, 2)
	if err != nil || n != 2 {
		t.Fatalf("bounded retention %d %v", n, err)
	}
	n, err = audit.DeleteExpiredWriteOperations(t.Context(), cutoff, 1000)
	if err != nil || n != 2 {
		t.Fatalf("remaining retention %d %v", n, err)
	}
	page, err = audit.ListWriteOperations(t.Context(), tenant, writeaudit.Filter{})
	if err != nil || len(page.Data) != 1 || page.Data[0].Action != "create" {
		t.Fatalf("creator retention %+v %v", page, err)
	}
	// Deleting the business resource cannot cascade through provenance.
	exec(t, pool, "DELETE FROM agents WHERE tenant_id=$1 AND id=$2", tenant, id)
	owners, err := audit.GetResourceOwners(t.Context(), tenant, "agent", []string{id})
	if err != nil || owners[0].APIKey == nil {
		t.Fatalf("deleted creator %+v %v", owners, err)
	}
	for _, query := range []struct {
		tenant, kind string
		ids          []string
	}{{"tenant", "agent", []string{id}}, {tenant, "project", []string{id}}, {tenant, "agent", nil}, {tenant, "agent", []string{""}}} {
		if _, err := audit.GetResourceOwners(t.Context(), query.tenant, query.kind, query.ids); !errors.Is(err, writeaudit.ErrInvalidQuery) {
			t.Fatalf("owner query %+v: %v", query, err)
		}
	}
}

func TestAdminAuditCursorAndFilters(t *testing.T) {
	pool, audit := openAudit(t)
	p := createProject(t, pool)
	for range 3 {
		if err := record(t, pool, adminaudit.WithSource(t.Context(), adminSource(p.id)), func(ctx context.Context, q *sqlc.Queries) error {
			return auditpg.RecordAdminMutation(ctx, q, p.tenant, "update", "agent", "agent-"+p.id)
		}); err != nil {
			t.Fatal(err)
		}
	}
	first, err := audit.ListAdminAudit(t.Context(), adminaudit.Filter{ProjectID: p.id, Limit: 2})
	if err != nil || len(first.Data) != 2 || !first.HasMore || first.NextCursor == "" {
		t.Fatalf("first %+v %v", first, err)
	}
	rest, err := audit.ListAdminAudit(t.Context(), adminaudit.Filter{ProjectID: p.id, Limit: 2, After: first.NextCursor})
	if err != nil || len(rest.Data) != 1 || rest.HasMore || rest.Data[0].ID == first.Data[0].ID || rest.Data[0].ID == first.Data[1].ID {
		t.Fatalf("rest %+v %v", rest, err)
	}
	for _, filter := range []adminaudit.Filter{{ProjectID: p.id, Limit: 2, After: first.NextCursor, Action: "delete"}, {After: "malformed"}, {Limit: 101}, {ProjectID: strings.Repeat("x", 129)}} {
		if _, err := audit.ListAdminAudit(t.Context(), filter); !errors.Is(err, adminaudit.ErrInvalidQuery) {
			t.Fatalf("filter %+v: %v", filter, err)
		}
	}
}
