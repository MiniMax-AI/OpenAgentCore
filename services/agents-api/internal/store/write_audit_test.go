package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/writeaudit"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func auditTestSource(tenant string) writeaudit.Source {
	digest := projectKeyDigest(uuid.NewString())
	return writeaudit.Source{KeyID: "static:" + digest, Prefix: digest[:8], Name: "test key", Kind: "static", TenantID: tenant, RequestID: uuid.NewString(), TraceID: uuid.NewString()}
}

func auditTestRecord(t *testing.T, s *Store, source writeaudit.Source, action, kind, id string, created ...AuditResource) {
	t.Helper()
	ctx := writeaudit.WithSource(t.Context(), source)
	if err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		return recordWriteAudit(ctx, s.queries.WithTx(tx), source.TenantID, action, kind, id, "", created...)
	}); err != nil {
		t.Fatal(err)
	}
}

func TestWriteAuditAtomicCommitAndRollback(t *testing.T) {
	s, pool := testStore(t)
	tenant := uuid.NewString()
	source := auditTestSource(tenant)
	for _, failure := range []string{"", "after_audit", "invalid_source", "database_audit_failure"} {
		t.Run(failure, func(t *testing.T) {
			id := uuid.NewString()
			source.RequestID = uuid.NewString()
			if failure == "invalid_source" {
				source.TraceID = ""
			} else {
				source.TraceID = uuid.NewString()
			}
			ctx := writeaudit.WithSource(t.Context(), source)
			err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
				q := s.queries.WithTx(tx)
				tenantUUID, _ := parseID(tenant)
				_, err := q.CreateAgent(ctx, sqlc.CreateAgentParams{ID: pgtype.UUID{Bytes: uuid.MustParse(id), Valid: true}, TenantID: tenantUUID, Metadata: []byte("{}"), Configuration: []byte("{}")})
				if err != nil {
					return err
				}
				if failure == "database_audit_failure" {
					// This transaction-local constraint deliberately rejects the audit insert.
					if _, err := tx.Exec(ctx, "ALTER TABLE write_audit_operations ADD CONSTRAINT audit_test_failure CHECK (false) NOT VALID"); err != nil {
						return err
					}
				}
				if err := recordWriteAudit(ctx, q, tenant, "create", "agent", id, "", AuditResource{Type: "agent", ID: id}); err != nil {
					return err
				}
				if failure == "after_audit" {
					return errors.New("business failure after audit")
				}
				return nil
			})
			if (err != nil) != (failure != "") {
				t.Fatalf("commit result: %v", err)
			}
			_, agentErr := s.GetAgent(t.Context(), tenant, id)
			if failure == "" && agentErr != nil || failure != "" && !errors.Is(agentErr, ErrNotFound) {
				t.Fatalf("business rollback: %v", agentErr)
			}
			page, err := s.ListWriteOperations(t.Context(), tenant, WriteOperationFilter{ResourceID: id})
			if err != nil {
				t.Fatal(err)
			}
			want := 0
			if failure == "" {
				want = 1
			}
			if len(page.Data) != want {
				t.Fatalf("audit count %d want %d", len(page.Data), want)
			}
			owners, err := s.GetResourceOwners(t.Context(), tenant, "agent", []string{id})
			if err != nil || (owners[0].APIKey != nil) != (failure == "") {
				t.Fatalf("ownership rollback: %+v %v", owners, err)
			}
		})
	}
	// A context without authenticated provenance is an intentional internal write.
	if err := recordWriteAudit(context.Background(), nil, tenant, "create", "agent", uuid.NewString(), ""); err != nil {
		t.Fatal(err)
	}
}

func TestWriteAuditOwnersIdentityReplayAndRevocation(t *testing.T) {
	s, _ := testStore(t)
	principal := projectKeyPrincipal()
	tenant := principal.TenantID
	a := auditTestSource(tenant)
	id, implicit := uuid.NewString(), uuid.NewString()
	auditTestRecord(t, s, a, "create", "session", id, AuditResource{Type: "session", ID: id}, AuditResource{Type: "environment", ID: implicit, ParentID: id})
	// Same request may reach a commit receipt twice but cannot create another owner.
	replayID := uuid.NewString()
	auditTestRecord(t, s, a, "create", "session", id, AuditResource{Type: "session", ID: replayID})
	b := auditTestSource(tenant)
	b.Kind = "console"
	auditTestRecord(t, s, b, "update", "session", id)
	owners, err := s.GetResourceOwners(t.Context(), tenant, "session", []string{replayID, id, id, "historical"})
	if err != nil || len(owners) != 4 || owners[0].APIKey != nil || owners[1].APIKey.ID != a.KeyID || owners[2].APIKey.ID != a.KeyID || owners[3].APIKey != nil {
		t.Fatalf("owners %+v: %v", owners, err)
	}
	implicitOwners, err := s.GetResourceOwners(t.Context(), tenant, "environment", []string{implicit})
	if err != nil || implicitOwners[0].APIKey.ID != a.KeyID {
		t.Fatalf("implicit owner: %+v %v", implicitOwners, err)
	}
	foreign, err := s.GetResourceOwners(t.Context(), uuid.NewString(), "session", []string{id})
	if err != nil || foreign[0].APIKey != nil {
		t.Fatalf("foreign owner: %+v %v", foreign, err)
	}
	page, err := s.ListWriteOperations(t.Context(), tenant, WriteOperationFilter{ResourceID: id})
	if err != nil || len(page.Data) != 2 || page.Data[0].APIKey.Kind != "console" || page.Data[1].APIKey.ID != a.KeyID {
		t.Fatalf("request dedup or key identity: %+v %v", page, err)
	}
	binding := projectKeyDigest("audit issuer")
	issued, err := s.CreateProjectAPIKey(t.Context(), binding, principal, uuid.NewString(), "issued key")
	if err != nil {
		t.Fatal(err)
	}
	c := auditTestSource(tenant)
	c.KeyID, c.Name, c.Prefix, c.Kind = issued.ID, issued.Name, issued.Prefix, "issued"
	fileID := "file_" + uuid.NewString()
	auditTestRecord(t, s, c, "create", "file", fileID, AuditResource{Type: "file", ID: fileID})
	if err := s.RevokeProjectAPIKey(t.Context(), binding, principal, issued.ID); err != nil {
		t.Fatal(err)
	}
	revoked, err := s.GetResourceOwners(t.Context(), tenant, "file", []string{fileID})
	if err != nil || revoked[0].APIKey.RevokedAt == nil || revoked[0].APIKey.Name != issued.Name {
		t.Fatalf("revocation metadata: %+v %v", revoked, err)
	}
	page, err = s.ListWriteOperations(t.Context(), tenant, WriteOperationFilter{KeyID: c.KeyID})
	if err != nil || len(page.Data) != 1 || page.Data[0].APIKey.RevokedAt == nil {
		t.Fatalf("history revocation: %+v %v", page, err)
	}
}

func TestWriteAuditCursorFiltersAndRetention(t *testing.T) {
	s, pool := testStore(t)
	tenant := uuid.NewString()
	source := auditTestSource(tenant)
	agent, err := s.CreateAgent(t.Context(), tenant, CreateAgentInput{Configuration: []byte("{}")})
	if err != nil {
		t.Fatal(err)
	}
	id := agent.ID
	auditTestRecord(t, s, source, "create", "agent", id, AuditResource{Type: "agent", ID: id})
	for i := 0; i < 4; i++ {
		source.RequestID = uuid.NewString()
		auditTestRecord(t, s, source, "update", "agent", id)
	}
	// Equal timestamps exercise the ID tie breaker, independent of insertion order.
	stamp := time.Date(1800, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, err := pool.Exec(t.Context(), "UPDATE write_audit_operations SET created_at=$1 WHERE tenant_id=$2", stamp, tenant); err != nil {
		t.Fatal(err)
	}
	first, err := s.ListWriteOperations(t.Context(), tenant, WriteOperationFilter{Limit: 2})
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
		page, err = s.ListWriteOperations(t.Context(), tenant, WriteOperationFilter{Limit: 2, After: page.NextCursor})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(seen) != 5 {
		t.Fatalf("lost rows %d", len(seen))
	}
	for _, filter := range []WriteOperationFilter{{Limit: 2, After: first.NextCursor, KeyID: "different"}, {After: "malformed"}} {
		if _, err := s.ListWriteOperations(t.Context(), tenant, filter); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("cursor filter: %v", err)
		}
	}
	if _, err := s.ListWriteOperations(t.Context(), uuid.NewString(), WriteOperationFilter{After: first.NextCursor}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("cross tenant cursor: %v", err)
	}
	for _, filter := range []WriteOperationFilter{{CreatedBefore: &stamp}, {KeyID: "missing"}, {ResourceType: "file"}, {ResourceID: "missing"}} {
		page, err := s.ListWriteOperations(t.Context(), tenant, filter)
		if err != nil || len(page.Data) != 0 {
			t.Fatalf("filter %+v: %+v %v", filter, page, err)
		}
	}
	inclusive, err := s.ListWriteOperations(t.Context(), tenant, WriteOperationFilter{CreatedAfter: &stamp})
	if err != nil || len(inclusive.Data) != 5 {
		t.Fatalf("inclusive lower bound: %+v %v", inclusive, err)
	}
	cutoff := stamp.Add(time.Hour)
	n, err := s.DeleteExpiredWriteOperations(t.Context(), cutoff, 2)
	if err != nil || n != 2 {
		t.Fatalf("bounded retention %d %v", n, err)
	}
	n, err = s.DeleteExpiredWriteOperations(t.Context(), cutoff, 1000)
	if err != nil || n != 2 {
		t.Fatalf("remaining retention %d %v", n, err)
	}
	page, err = s.ListWriteOperations(t.Context(), tenant, WriteOperationFilter{})
	if err != nil || len(page.Data) != 1 || page.Data[0].Action != "create" {
		t.Fatalf("creator retention %+v %v", page, err)
	}
	// Deleting the business resource cannot cascade through provenance.
	if _, err := pool.Exec(t.Context(), "DELETE FROM agents WHERE tenant_id=$1 AND id=$2", tenant, id); err != nil {
		t.Fatal(err)
	}
	owners, err := s.GetResourceOwners(t.Context(), tenant, "agent", []string{id})
	if err != nil || owners[0].APIKey == nil {
		t.Fatalf("deleted creator %+v %v", owners, err)
	}
}

func TestWriteAuditSourceValidation(t *testing.T) {
	valid := auditTestSource(uuid.NewString())
	for _, field := range []string{"tenant", "key", "prefix", "kind", "request", "trace", "name"} {
		source := valid
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
		}
		ctx := writeaudit.WithSource(t.Context(), source)
		if err := recordWriteAudit(ctx, nil, valid.TenantID, "create", "agent", "resource", ""); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("%s accepted: %v", field, err)
		}
	}
}
