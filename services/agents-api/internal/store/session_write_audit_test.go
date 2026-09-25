package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/writeaudit"
	"github.com/google/uuid"
)

func sessionAuditContext(t *testing.T, tenant, key string) context.Context {
	t.Helper()
	digest := strings.Repeat(key, 64)
	return writeaudit.WithSource(t.Context(), writeaudit.Source{TenantID: tenant, KeyID: "static:" + digest,
		Name: "safe key", Prefix: digest[:8], Kind: "static", RequestID: uuid.NewString(), TraceID: "shared-trace"})
}

func sessionAuditCount(t *testing.T, s *Store, tenant string, want int) {
	t.Helper()
	var count int
	if err := s.pool.QueryRow(t.Context(), "SELECT count(*) FROM write_audit_operations WHERE tenant_id=$1", tenant).Scan(&count); err != nil || count != want {
		t.Fatalf("audit count = %d, want %d: %v", count, want, err)
	}
}

func rejectSessionAudit(t *testing.T, s *Store, ctx context.Context) {
	t.Helper()
	source, _ := writeaudit.FromContext(ctx)
	name := "reject_session_audit_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err := s.pool.Exec(t.Context(), fmt.Sprintf(`CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN IF NEW.request_id = '%s' THEN RAISE EXCEPTION 'audit insert rejected'; END IF; RETURN NEW; END $$;
CREATE TRIGGER %s BEFORE INSERT ON write_audit_operations FOR EACH ROW EXECUTE FUNCTION %s();`, name, source.RequestID, name, name))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := s.pool.Exec(context.Background(), fmt.Sprintf("DROP TRIGGER %s ON write_audit_operations; DROP FUNCTION %s();", name, name)); err != nil {
			t.Error(err)
		}
	})
}

func TestSessionWriteAuditCreationReplayNoopAndDeletion(t *testing.T) {
	s, _ := testStore(t)
	tenant := uuid.NewString()
	input := environmentInput("audit-create", "self_hosted", "/workspace")
	created, err := s.CreateSession(sessionAuditContext(t, tenant, "a"), tenant, input)
	if err != nil {
		t.Fatal(err)
	}
	env, err := s.GetSessionEnvironment(t.Context(), tenant, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateSession(sessionAuditContext(t, tenant, "b"), tenant, input); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetSession(sessionAuditContext(t, tenant, "b"), tenant, created.ID); err != nil {
		t.Fatal(err)
	}
	sessionAuditCount(t, s, tenant, 2)
	for _, resource := range []string{created.ID, env.ID} {
		var key string
		if err := s.pool.QueryRow(t.Context(), `SELECT o.key_id FROM write_audit_owners a JOIN write_audit_operations o ON o.id=a.operation_id WHERE a.tenant_id=$1 AND a.resource_id=$2`, tenant, resource).Scan(&key); err != nil || key != "static:"+strings.Repeat("a", 64) {
			t.Fatal("retry replaced creator", key, err)
		}
	}
	for _, action := range []string{"create", "send_events"} {
		if err := s.AuditSessionOperation(sessionAuditContext(t, tenant, "b"), tenant, created.ID, action); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.UpdateSessionMetadata(sessionAuditContext(t, tenant, "b"), tenant, created.ID, map[string]string{"private": "not in audit"}); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := s.DeleteSession(sessionAuditContext(t, tenant, "b"), tenant, created.ID); err != nil {
			t.Fatal(err)
		}
	}
	sessionAuditCount(t, s, tenant, 7)
	var history string
	if err := s.pool.QueryRow(t.Context(), "SELECT jsonb_agg(to_jsonb(o))::text FROM write_audit_operations o WHERE tenant_id=$1", tenant).Scan(&history); err != nil || strings.Contains(history, "not in audit") || strings.Contains(history, "/workspace") {
		t.Fatal("payload entered audit", err)
	}
	if err := s.AuditSessionOperation(sessionAuditContext(t, tenant, "b"), tenant, created.ID, "send_events"); !errors.Is(err, ErrNotFound) {
		t.Fatal("deleted no-op accepted", err)
	}
	sessionAuditCount(t, s, tenant, 7)
}

func TestSessionWriteAuditConcurrentCreation(t *testing.T) {
	s, _ := testStore(t)
	tenant := uuid.NewString()
	input := environmentInput("audit-race", "self_hosted", "/workspace")
	var group sync.WaitGroup
	for range 8 {
		ctx := sessionAuditContext(t, tenant, "a")
		group.Go(func() {
			if _, err := s.CreateSession(ctx, tenant, input); err != nil {
				t.Error(err)
			}
		})
	}
	group.Wait()
	sessionAuditCount(t, s, tenant, 8)
	var owners, creates int
	if err := s.pool.QueryRow(t.Context(), `SELECT count(*),count(DISTINCT operation_id) FROM write_audit_owners WHERE tenant_id=$1`, tenant).Scan(&owners, &creates); err != nil || owners != 2 || creates != 1 {
		t.Fatal("creation race attribution", owners, creates, err)
	}
}

func TestSessionWriteAuditRollback(t *testing.T) {
	for _, operation := range []string{"create", "update", "delete", "events", "noop", "reserve"} {
		t.Run(operation, func(t *testing.T) {
			s, _ := testStore(t)
			tenant := uuid.NewString()
			input := environmentInput("audit-rollback", "self_hosted", "/workspace")
			ctx := sessionAuditContext(t, tenant, "a")
			rejectSessionAudit(t, s, ctx)
			if operation == "create" {
				if _, err := s.CreateSession(ctx, tenant, input); err == nil {
					t.Fatal("creation bypassed audit failure")
				}
				var count int
				if err := s.pool.QueryRow(t.Context(), "SELECT count(*) FROM sessions WHERE tenant_id=$1", tenant).Scan(&count); err != nil || count != 0 {
					t.Fatal("creation did not roll back", count, err)
				}
				return
			}
			created, err := s.CreateSession(t.Context(), tenant, input)
			if err != nil {
				t.Fatal(err)
			}
			switch operation {
			case "update":
				_, err = s.UpdateSessionMetadata(ctx, tenant, created.ID, map[string]string{"new": "value"})
			case "delete":
				err = s.DeleteSession(ctx, tenant, created.ID)
			case "events":
				_, err = s.SubmitInputs(ctx, tenant, created.ID, "events", []Input{messageInput("private")})
			case "noop":
				err = s.AuditSessionOperation(ctx, tenant, created.ID, "send_events")
			case "reserve":
				_, err = s.ReserveEnvironmentInput(ctx, tenant, created.ID, "reserve", []Input{messageInput("private")})
			}
			if err == nil {
				t.Fatal("mutation bypassed audit failure")
			}
			got, err := s.GetSession(t.Context(), tenant, created.ID)
			if err != nil || len(got.Metadata) != 0 {
				t.Fatal("resource update/deletion survived rollback", got, err)
			}
			var inputs, reservations int
			if err := s.pool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM turn_inputs WHERE session_id=$1),(SELECT count(*) FROM environment_input_reservations WHERE session_id=$1)`, created.ID).Scan(&inputs, &reservations); err != nil || inputs != 0 || reservations != 0 {
				t.Fatal("input survived rollback", inputs, reservations, err)
			}
			sessionAuditCount(t, s, tenant, 0)
		})
	}
}

func TestEventsWriteAuditAdmissionAndReplay(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			s, _ := testStore(t)
			tenant := uuid.NewString()
			input := environmentInput("audit-events", "self_hosted", "/workspace")
			created, err := s.CreateSession(t.Context(), tenant, input)
			if err != nil {
				t.Fatal(err)
			}
			submit := func(ctx context.Context) error {
				if prepared {
					_, err := s.ReserveEnvironmentInput(ctx, tenant, created.ID, "batch", []Input{messageInput("private")})
					return err
				}
				_, err := s.SubmitInputs(ctx, tenant, created.ID, "batch", []Input{messageInput("private")})
				return err
			}
			first := sessionAuditContext(t, tenant, "a")
			for _, ctx := range []context.Context{first, first, sessionAuditContext(t, tenant, "b")} {
				if err := submit(ctx); err != nil {
					t.Fatal(err)
				}
			}
			sessionAuditCount(t, s, tenant, 2)
			var owners int
			if err := s.pool.QueryRow(t.Context(), "SELECT count(*) FROM write_audit_owners WHERE tenant_id=$1", tenant).Scan(&owners); err != nil || owners != 0 {
				t.Fatal("events acquired historical ownership", owners, err)
			}
		})
	}
}

func TestSessionWriteAuditInitialInputAndHistoricalReplay(t *testing.T) {
	for _, kind := range []string{"none", "self_hosted"} {
		t.Run(kind, func(t *testing.T) {
			s, _ := testStore(t)
			tenant := uuid.NewString()
			input := environmentInput("audit-initial", kind, "/workspace")
			input.InitialInputs = []Input{messageInput("private initial message")}
			created, err := s.CreateSessionStream(sessionAuditContext(t, tenant, "a"), tenant, input)
			if err != nil || !created.Created {
				t.Fatal(created, err)
			}
			sessionAuditCount(t, s, tenant, 1)
			input.IdempotencyKey = "legacy"
			legacy, err := s.CreateSession(t.Context(), tenant, input)
			if err != nil {
				t.Fatal(err)
			}
			if err := s.AuditSessionOperation(sessionAuditContext(t, tenant, "b"), tenant, legacy.ID, "create"); err != nil {
				t.Fatal(err)
			}
			var owners int
			if err := s.pool.QueryRow(t.Context(), "SELECT count(*) FROM write_audit_owners WHERE tenant_id=$1 AND resource_id=$2", tenant, legacy.ID).Scan(&owners); err != nil || owners != 0 {
				t.Fatal("replay attributed historical resource", owners, err)
			}
			sessionAuditCount(t, s, tenant, 2)
		})
	}
}
