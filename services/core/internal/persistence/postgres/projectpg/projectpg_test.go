package projectpg_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/adminaudit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgtest"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/projectpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/projects"
)

func newProjects(t *testing.T, pool *pgxpool.Pool) (*projects.Service, *projectpg.Store) {
	t.Helper()
	store := projectpg.New(pgunit.NewPool(pool))
	service, err := projects.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	return service, store
}

func adminContext(ctx context.Context, projectID string) context.Context {
	return adminaudit.WithSource(ctx, adminaudit.Source{CredentialID: "12345678", ActorLabel: "test", RequestID: uuid.NewString(), TraceID: uuid.NewString(), ProjectID: projectID})
}

func createProject(t *testing.T, service *projects.Service) projects.Project {
	t.Helper()
	id := uuid.NewString()
	p, err := service.CreateProject(adminContext(t.Context(), id), projects.CreateProject{ID: id, Name: "Project"})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func createKey(t *testing.T, service *projects.Service, projectID, name string) projects.IssuedAPIKey {
	t.Helper()
	key, err := service.CreateAPIKey(adminContext(t.Context(), projectID), projects.CreateAPIKey{ProjectID: projectID, ID: uuid.NewString(), Name: name})
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func digest(secret string) [sha256.Size]byte { return sha256.Sum256([]byte(secret)) }

// insertAsset stands in for any tenant-owned asset; archiving must retain it.
func insertAsset(t *testing.T, pool *pgxpool.Pool, tenant string) string {
	t.Helper()
	id := uuid.NewString()
	if _, err := pool.Exec(t.Context(), `INSERT INTO agents (id, tenant_id, metadata, configuration) VALUES ($1, $2, '{}', '{"model":"test"}')`, id, tenant); err != nil {
		t.Fatal(err)
	}
	return id
}

func assetTenant(t *testing.T, pool *pgxpool.Pool, id string) string {
	t.Helper()
	var tenant string
	if err := pool.QueryRow(t.Context(), `SELECT tenant_id::text FROM agents WHERE id = $1`, id).Scan(&tenant); err != nil {
		t.Fatal("asset missing", err)
	}
	return tenant
}

func TestKeysShareTheProjectPrincipalAndArchiveRetainsAssets(t *testing.T) {
	pool := pgtest.Open(t)
	service, reader := newProjects(t, pool)
	p := createProject(t, service)
	other := createProject(t, service)
	first := createKey(t, service, p.ID, "first")
	second := createKey(t, service, p.ID, "second")
	a, err := reader.ResolveAPIKey(t.Context(), digest(first.Key))
	if err != nil {
		t.Fatal(err)
	}
	b, err := reader.ResolveAPIKey(t.Context(), digest(second.Key))
	if err != nil || a.Principal != b.Principal || a.Principal.SubjectID != "project:"+p.ID || a.Principal.ProjectID != "proj_"+p.ID || a.Principal.OrganizationID != "core" || a.Principal.SubjectKind != "service_account" {
		t.Fatal("Project keys do not share the stable Project principal", err)
	}
	if a.Principal.TenantID != p.TenantID || other.TenantID == p.TenantID {
		t.Fatal("Project tenants are not separate")
	}
	asset := insertAsset(t, pool, a.Principal.TenantID)
	renamed, err := service.RenameProject(adminContext(t.Context(), p.ID), projects.RenameProject{ID: p.ID, Name: "renamed"})
	if err != nil || renamed.TenantID != p.TenantID || renamed.ActiveKeyCount != 2 || renamed.Name != "renamed" {
		t.Fatal("rename changed Project ownership", err)
	}
	afterRename, err := reader.ResolveAPIKey(t.Context(), digest(first.Key))
	if err != nil || afterRename.Principal != a.Principal {
		t.Fatal("rename changed key principal", err)
	}
	listed, err := reader.ListAPIKeys(t.Context(), p.ID, projects.ListQuery{Limit: 50, Ascending: true})
	if err != nil || len(listed.Data) != 2 {
		t.Fatal("Project keys missing", err)
	}
	firstDigest := digest(first.Key)
	raw, _ := json.Marshal(listed)
	if strings.Contains(string(raw), first.Key) || strings.Contains(string(raw), hex.EncodeToString(firstDigest[:])) {
		t.Fatal("key list exposed credential")
	}
	if err := service.RevokeAPIKey(adminContext(t.Context(), other.ID), projects.RevokeAPIKey{ProjectID: other.ID, ID: first.ID}); !errors.Is(err, projects.ErrNotFound) {
		t.Fatal("foreign Project revoked key", err)
	}
	for range 2 {
		if err := service.RevokeAPIKey(adminContext(t.Context(), p.ID), projects.RevokeAPIKey{ProjectID: p.ID, ID: first.ID}); err != nil {
			t.Fatal("revocation is not idempotent", err)
		}
	}
	if _, err := reader.ResolveAPIKey(t.Context(), digest(first.Key)); !errors.Is(err, projects.ErrNotFound) {
		t.Fatal("revoked key authenticated")
	}
	if _, err := reader.ResolveAPIKey(t.Context(), digest(second.Key)); err != nil {
		t.Fatal("revocation affected peer", err)
	}
	archived, err := service.ArchiveProject(adminContext(t.Context(), p.ID), projects.ArchiveProject{ID: p.ID})
	if err != nil || archived.ArchivedAt == nil || archived.ActiveKeyCount != 0 {
		t.Fatal("archive did not revoke all keys", err)
	}
	if _, err := reader.ResolveAPIKey(t.Context(), digest(second.Key)); !errors.Is(err, projects.ErrNotFound) {
		t.Fatal("archived Project authenticated")
	}
	if _, err := service.CreateAPIKey(adminContext(t.Context(), p.ID), projects.CreateAPIKey{ProjectID: p.ID, ID: uuid.NewString(), Name: "late"}); !errors.Is(err, projects.ErrArchived) {
		t.Fatal("archived Project admitted new key", err)
	}
	if assetTenant(t, pool, asset) != p.TenantID {
		t.Fatal("archive changed asset ownership")
	}
	// Credential separation covers revoked keys and archived Projects.
	for _, secret := range []string{first.Key, second.Key} {
		if exists, err := reader.APIKeyDigestExists(t.Context(), digest(secret)); err != nil || !exists {
			t.Fatal("persisted digest not found", err)
		}
	}
	if exists, err := reader.APIKeyDigestExists(t.Context(), digest(uuid.NewString())); err != nil || exists {
		t.Fatal("unknown digest found", err)
	}
	raw, _ = json.Marshal(archived)
	if strings.Contains(string(raw), p.TenantID) {
		t.Fatal("Project response exposed internal tenant")
	}
}

func TestManagementRequiresAtomicAudit(t *testing.T) {
	service, reader := newProjects(t, pgtest.Open(t))
	id := uuid.NewString()
	if _, err := service.CreateProject(t.Context(), projects.CreateProject{ID: id, Name: "unaudited"}); !errors.Is(err, adminaudit.ErrInvalidSource) {
		t.Fatal("unaudited Project accepted", err)
	}
	if _, err := reader.GetProject(t.Context(), id); !errors.Is(err, projects.ErrNotFound) {
		t.Fatal("unaudited Project persisted")
	}
	p := createProject(t, service)
	if _, err := service.CreateAPIKey(t.Context(), projects.CreateAPIKey{ProjectID: p.ID, ID: uuid.NewString(), Name: "unaudited"}); !errors.Is(err, adminaudit.ErrInvalidSource) {
		t.Fatal("unaudited key accepted", err)
	}
	page, err := reader.ListAPIKeys(t.Context(), p.ID, projects.ListQuery{Limit: 20, Ascending: true})
	if err != nil || len(page.Data) != 0 {
		t.Fatal("unaudited key persisted")
	}
	if _, err := service.RenameProject(t.Context(), projects.RenameProject{ID: p.ID, Name: "bad"}); !errors.Is(err, adminaudit.ErrInvalidSource) {
		t.Fatal("unaudited rename accepted", err)
	}
	if _, err := service.ArchiveProject(t.Context(), projects.ArchiveProject{ID: p.ID}); !errors.Is(err, adminaudit.ErrInvalidSource) {
		t.Fatal("unaudited archive accepted", err)
	}
	current, err := reader.GetProject(t.Context(), p.ID)
	if err != nil || current.Project.Name != p.Name || current.Project.ArchivedAt != nil {
		t.Fatal("unaudited mutation persisted")
	}
}

func TestMalformedIdentifiers(t *testing.T) {
	service, reader := newProjects(t, pgtest.Open(t))
	if _, err := service.CreateProject(adminContext(t.Context(), uuid.NewString()), projects.CreateProject{ID: "not-a-uuid", Name: "Project"}); !errors.Is(err, projects.ErrInvalidInput) {
		t.Fatal("malformed Project ID accepted", err)
	}
	p := createProject(t, service)
	if _, err := service.CreateAPIKey(adminContext(t.Context(), p.ID), projects.CreateAPIKey{ProjectID: p.ID, ID: "not-a-uuid", Name: "key"}); !errors.Is(err, projects.ErrInvalidInput) {
		t.Fatal("malformed key ID accepted", err)
	}
	for _, id := range []string{"not-a-uuid", uuid.Nil.String()} {
		if _, err := reader.GetProject(t.Context(), id); !errors.Is(err, projects.ErrNotFound) {
			t.Fatal("malformed Project ID found", err)
		}
		if _, err := reader.ListProjects(t.Context(), projects.ListQuery{After: id, Limit: 1}); !errors.Is(err, projects.ErrNotFound) {
			t.Fatal("malformed Project cursor accepted", err)
		}
		if _, err := reader.ListAPIKeys(t.Context(), p.ID, projects.ListQuery{After: id, Limit: 1}); !errors.Is(err, projects.ErrNotFound) {
			t.Fatal("malformed key cursor accepted", err)
		}
		if err := service.RevokeAPIKey(adminContext(t.Context(), p.ID), projects.RevokeAPIKey{ProjectID: p.ID, ID: id}); !errors.Is(err, projects.ErrNotFound) {
			t.Fatal("malformed key revoked", err)
		}
		if _, err := service.ArchiveProject(adminContext(t.Context(), p.ID), projects.ArchiveProject{ID: id}); !errors.Is(err, projects.ErrNotFound) {
			t.Fatal("malformed Project archived", err)
		}
	}
}

func TestCatalogPaginationAndScopedKeyCursor(t *testing.T) {
	service, reader := newProjects(t, pgtest.Open(t))
	first, second := createProject(t, service), createProject(t, service)
	if _, err := service.CreateProject(adminContext(t.Context(), first.ID), projects.CreateProject{ID: first.ID, Name: "duplicate"}); !errors.Is(err, projects.ErrExists) {
		t.Fatal("duplicate Project ID did not conflict", err)
	}
	// Equal display names do not merge Projects or keys.
	a := createKey(t, service, first.ID, "same name")
	b := createKey(t, service, first.ID, "same name")
	if _, err := service.CreateAPIKey(adminContext(t.Context(), first.ID), projects.CreateAPIKey{ProjectID: first.ID, ID: a.ID, Name: "duplicate ID"}); !errors.Is(err, projects.ErrAPIKeyExists) {
		t.Fatal("duplicate key ID did not conflict", err)
	}
	page, err := reader.ListAPIKeys(t.Context(), first.ID, projects.ListQuery{Limit: 1, Ascending: true})
	if err != nil || len(page.Data) != 1 || !page.HasMore {
		t.Fatal("first key page invalid", err)
	}
	tail, err := reader.ListAPIKeys(t.Context(), first.ID, projects.ListQuery{After: page.Data[0].ID, Limit: 1, Ascending: true})
	if err != nil || len(tail.Data) != 1 || tail.HasMore || tail.Data[0].ID <= page.Data[0].ID {
		t.Fatal("key cursor did not advance", err)
	}
	reverse, err := reader.ListAPIKeys(t.Context(), first.ID, projects.ListQuery{Limit: 1})
	if err != nil || len(reverse.Data) != 1 || reverse.Data[0].ID != tail.Data[0].ID {
		t.Fatal("descending key page invalid", err)
	}
	if _, err := reader.ListAPIKeys(t.Context(), second.ID, projects.ListQuery{After: a.ID, Limit: 1, Ascending: true}); !errors.Is(err, projects.ErrNotFound) {
		t.Fatal("foreign key cursor accepted", err)
	}
	if _, err := reader.ListAPIKeys(t.Context(), uuid.NewString(), projects.ListQuery{Limit: 1}); !errors.Is(err, projects.ErrNotFound) {
		t.Fatal("missing Project listed keys", err)
	}
	if _, err := reader.ListAPIKeys(t.Context(), first.ID, projects.ListQuery{Limit: 101, Ascending: true}); !errors.Is(err, projects.ErrInvalidInput) {
		t.Fatal("oversized key page accepted", err)
	}
	if _, err := reader.ListProjects(t.Context(), projects.ListQuery{Limit: 101, Ascending: true}); !errors.Is(err, projects.ErrInvalidInput) {
		t.Fatal("oversized Project page accepted", err)
	}
	projectsPage, err := reader.ListProjects(t.Context(), projects.ListQuery{After: first.ID, Limit: 100, Ascending: first.ID < second.ID})
	if err != nil || !containsProject(projectsPage, second.ID) || containsProject(projectsPage, first.ID) {
		t.Fatal("Project cursor did not advance past itself", err)
	}
	for _, id := range []string{b.ID, a.ID} {
		if err := service.RevokeAPIKey(adminContext(t.Context(), first.ID), projects.RevokeAPIKey{ProjectID: first.ID, ID: id}); err != nil {
			t.Fatal(err)
		}
	}
	current, err := reader.GetProject(t.Context(), first.ID)
	if err != nil || current.Project.ArchivedAt != nil || current.Project.ActiveKeyCount != 0 {
		t.Fatal("last key removal changed Project lifecycle", err)
	}
	createKey(t, service, first.ID, "new access")
}

func containsProject(page projects.Page, id string) bool {
	for _, p := range page.Data {
		if p.ID == id {
			return true
		}
	}
	return false
}

// Key issuance holds a shared Project lock, so a key issued while the Project
// is being archived is either revoked by the archive or refused after it.
func TestArchiveRacesKeyIssuance(t *testing.T) {
	pool := pgtest.Open(t)
	service, reader := newProjects(t, pool)
	p := createProject(t, service)
	var wg sync.WaitGroup
	issued := make(chan projects.IssuedAPIKey, 16)
	for range 16 {
		wg.Go(func() {
			key, err := service.CreateAPIKey(adminContext(t.Context(), p.ID), projects.CreateAPIKey{ProjectID: p.ID, ID: uuid.NewString(), Name: "racing"})
			if err == nil {
				issued <- key
			} else if !errors.Is(err, projects.ErrArchived) {
				t.Error(err)
			}
		})
	}
	wg.Go(func() {
		if _, err := service.ArchiveProject(adminContext(t.Context(), p.ID), projects.ArchiveProject{ID: p.ID}); err != nil {
			t.Error(err)
		}
	})
	wg.Wait()
	close(issued)
	for key := range issued {
		if _, err := reader.ResolveAPIKey(t.Context(), digest(key.Key)); !errors.Is(err, projects.ErrNotFound) {
			t.Fatal("key issued during archive authenticates", err)
		}
	}
	current, err := reader.GetProject(t.Context(), p.ID)
	if err != nil || current.Project.ArchivedAt == nil || current.Project.ActiveKeyCount != 0 {
		t.Fatal("archived Project retains an active key", err)
	}
}

const rejectedRequest = "reject-admin-mutation-fixture"

// rejectAuditInsert makes the audit insert of one request fail. The sequence
// survives rollback, so it proves the mutation reached its audit insert.
func rejectAuditInsert(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	_, err := pool.Exec(t.Context(), `CREATE SEQUENCE admin_mutation_rejections;
 CREATE FUNCTION reject_admin_mutation_fixture() RETURNS trigger LANGUAGE plpgsql AS $$
 BEGIN IF NEW.request_id = 'reject-admin-mutation-fixture' THEN PERFORM nextval('admin_mutation_rejections'); RAISE EXCEPTION 'forced administrator audit failure'; END IF; RETURN NEW; END $$;
 CREATE TRIGGER reject_admin_mutation_fixture BEFORE INSERT ON admin_audit_log FOR EACH ROW EXECUTE FUNCTION reject_admin_mutation_fixture()`)
	if err != nil {
		t.Fatal(err)
	}
}

func auditRejections(t *testing.T, pool *pgxpool.Pool) int64 {
	t.Helper()
	var count int64
	if err := pool.QueryRow(t.Context(), "SELECT CASE WHEN is_called THEN last_value ELSE 0 END FROM admin_mutation_rejections").Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

// snapshot captures whole tables, so the test owns an isolated database.
func snapshot(t *testing.T, pool *pgxpool.Pool, tables ...string) map[string]string {
	t.Helper()
	result := make(map[string]string, len(tables))
	for _, table := range tables {
		var rows string
		if err := pool.QueryRow(t.Context(), "SELECT COALESCE(jsonb_agg(to_jsonb(r) ORDER BY to_jsonb(r)::text)::text,'[]') FROM "+pgx.Identifier{table}.Sanitize()+" r").Scan(&rows); err != nil {
			t.Fatalf("snapshot %s: %v", table, err)
		}
		result[table] = rows
	}
	return result
}

func TestFailedAuditRollsBackEveryMutation(t *testing.T) {
	pool := pgtest.OpenIsolated(t, nil)
	service, reader := newProjects(t, pool)
	rejectAuditInsert(t, pool)
	for _, action := range []string{"project_create", "rename", "archive", "key_create", "revoke"} {
		t.Run(action, func(t *testing.T) {
			projectID, keyID := uuid.NewString(), uuid.NewString()
			var p projects.Project
			var issued projects.IssuedAPIKey
			var asset string
			ctxFor := func(request string) context.Context {
				return adminaudit.WithSource(t.Context(), adminaudit.Source{CredentialID: "87654321", ActorLabel: "administrator fixture", ProjectID: projectID, RequestID: request, TraceID: "admin-mutation-trace"})
			}
			if action != "project_create" {
				var err error
				p, err = service.CreateProject(ctxFor(uuid.NewString()), projects.CreateProject{ID: projectID, Name: "original"})
				if err != nil {
					t.Fatal(err)
				}
				asset = insertAsset(t, pool, p.TenantID)
				if action == "archive" || action == "revoke" {
					issued, err = service.CreateAPIKey(ctxFor(uuid.NewString()), projects.CreateAPIKey{ProjectID: projectID, ID: keyID, Name: "original key"})
					if err != nil {
						t.Fatal(err)
					}
				}
			}
			mutate := func(request string) (projects.Project, projects.IssuedAPIKey, error) {
				ctx := ctxFor(request)
				switch action {
				case "project_create":
					v, e := service.CreateProject(ctx, projects.CreateProject{ID: projectID, Name: "created"})
					return v, projects.IssuedAPIKey{}, e
				case "rename":
					v, e := service.RenameProject(ctx, projects.RenameProject{ID: projectID, Name: "renamed"})
					return v, projects.IssuedAPIKey{}, e
				case "archive":
					v, e := service.ArchiveProject(ctx, projects.ArchiveProject{ID: projectID})
					return v, projects.IssuedAPIKey{}, e
				case "key_create":
					v, e := service.CreateAPIKey(ctx, projects.CreateAPIKey{ProjectID: projectID, ID: keyID, Name: "created key"})
					return projects.Project{}, v, e
				default:
					return projects.Project{}, projects.IssuedAPIKey{}, service.RevokeAPIKey(ctx, projects.RevokeAPIKey{ProjectID: projectID, ID: keyID})
				}
			}
			tables := []string{"execution_project_scopes", "projects", "project_api_keys", "agents", "admin_audit_log", "write_audit_operations", "write_audit_owners"}
			before := snapshot(t, pool, tables...)
			rejections := auditRejections(t, pool)
			rejectedProject, rejectedKey, err := mutate(rejectedRequest)
			if err == nil || auditRejections(t, pool) != rejections+1 {
				t.Fatal("mutation did not reach the failing final audit insertion", err)
			}
			if rejectedProject.ID != "" || rejectedKey.Key != "" {
				t.Fatal("failed transaction exposed an uncommitted resource")
			}
			if !reflect.DeepEqual(before, snapshot(t, pool, tables...)) {
				t.Fatal("failed audit changed Project, key, asset, or audit state")
			}
			if issued.Key != "" {
				if _, err := reader.ResolveAPIKey(t.Context(), digest(issued.Key)); err != nil {
					t.Fatal("failed audit invalidated original key", err)
				}
			}
			request := uuid.NewString()
			result, key, err := mutate(request)
			if err != nil {
				t.Fatal(err)
			}
			if action == "project_create" {
				p = result
			}
			expectedAction, kind, resource := action, "project", projectID
			switch action {
			case "project_create":
				expectedAction = "create"
			case "key_create":
				expectedAction = "create"
				kind = "api_key"
				resource = keyID
			case "revoke":
				kind = "api_key"
				resource = keyID
			}
			var count int
			if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM admin_audit_log WHERE project_id=$1 AND tenant_id=$2 AND request_id=$3 AND action=$4 AND resource_type=$5 AND resource_id=$6 AND admin_credential_id='87654321' AND actor_label='administrator fixture' AND trace_id='admin-mutation-trace'`, projectID, p.TenantID, request, expectedAction, kind, resource).Scan(&count); err != nil || count != 1 {
				t.Fatal("successful mutation lacks matching audit", err)
			}
			if action == "key_create" {
				if key.Key == "" {
					t.Fatal("issuance lacks plaintext")
				}
				if _, err := reader.ResolveAPIKey(t.Context(), digest(key.Key)); err != nil {
					t.Fatal(err)
				}
			}
			if action == "archive" || action == "revoke" {
				if _, err := reader.ResolveAPIKey(t.Context(), digest(issued.Key)); !errors.Is(err, projects.ErrNotFound) {
					t.Fatal("invalidated key authenticated", err)
				}
			}
			if action != "project_create" && assetTenant(t, pool, asset) != p.TenantID {
				t.Fatal("management mutation changed assets")
			}
		})
	}
}
