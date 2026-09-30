package filepg_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/adminaudit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/files"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/filepg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgtest"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/writeaudit"
)

// open returns the adapter and the files service over pool.
func open(t *testing.T, pool *pgxpool.Pool) (*filepg.Store, *files.Service) {
	t.Helper()
	store := filepg.New(pgunit.NewPool(pool))
	service, err := files.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	return store, service
}

func upload(data []byte) func(io.Writer) (files.Upload, error) {
	return func(w io.Writer) (files.Upload, error) {
		_, err := w.Write(data)
		return files.Upload{Filename: "source.bin", Purpose: files.PurposeUserData}, err
	}
}

func create(t *testing.T, service *files.Service, ctx context.Context, tenant string, data []byte) files.File {
	t.Helper()
	file, err := service.Create(ctx, files.CreateCommand{TenantID: tenant, Upload: upload(data)})
	if err != nil {
		t.Fatal(err)
	}
	return file
}

func bodyOID(t *testing.T, pool *pgxpool.Pool, tenant string, file files.File) uint32 {
	t.Helper()
	var oid uint32
	if err := pool.QueryRow(t.Context(), "SELECT body_oid FROM source_files WHERE tenant_id=$1 AND id=$2", tenant, strings.TrimPrefix(file.ID, "file-")).Scan(&oid); err != nil {
		t.Fatal(err)
	}
	return oid
}

func objectExists(t *testing.T, pool *pgxpool.Pool, oid uint32) bool {
	t.Helper()
	var exists bool
	if err := pool.QueryRow(t.Context(), "SELECT EXISTS (SELECT 1 FROM pg_largeobject_metadata WHERE oid=$1)", oid).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	return exists
}

func objectCount(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var count int
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM pg_largeobject_metadata").Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestFilesPersistScopeAndDelete(t *testing.T) {
	pool := pgtest.Open(t)
	store, service := open(t, pool)
	tenant, foreign := uuid.NewString(), uuid.NewString()
	for _, data := range [][]byte{{}, {0, 1, 255}, bytes.Repeat([]byte("binary\x00"), 300000)} {
		file := create(t, service, t.Context(), tenant, data)
		if file.SizeBytes != int64(len(data)) || file.Filename != "source.bin" || file.Purpose != files.PurposeUserData || !strings.HasPrefix(file.ID, "file-") || file.CreatedAt.IsZero() {
			t.Fatalf("create: %+v", file)
		}
		oid := bodyOID(t, pool, tenant, file)
		var digest string
		if err := pool.QueryRow(t.Context(), "SELECT sha256 FROM source_files WHERE body_oid=$1", oid).Scan(&digest); err != nil || digest != sha256Hex(data) {
			t.Fatal("stored digest differs", err)
		}
		if _, err := store.Get(t.Context(), foreign, file.ID); !errors.Is(err, files.ErrNotFound) {
			t.Fatalf("foreign metadata: %v", err)
		}
		if err := store.Read(t.Context(), foreign, file.ID, func(files.File, io.Reader) error {
			t.Fatal("foreign content callback reached")
			return nil
		}); !errors.Is(err, files.ErrNotFound) {
			t.Fatalf("foreign content: %v", err)
		}
		if err := service.Delete(t.Context(), files.DeleteCommand{TenantID: foreign, FileID: file.ID}); !errors.Is(err, files.ErrNotFound) {
			t.Fatalf("foreign delete: %v", err)
		}
		reopened, _ := open(t, pool)
		if got, err := reopened.Get(t.Context(), tenant, file.ID); err != nil || got != file {
			t.Fatalf("metadata: %+v %v", got, err)
		}
		if err := reopened.Read(t.Context(), tenant, file.ID, func(meta files.File, r io.Reader) error {
			got, err := io.ReadAll(r)
			if meta != file || !bytes.Equal(got, data) {
				t.Error("persisted contents differ")
			}
			return err
		}); err != nil {
			t.Fatal(err)
		}
		if err := service.Delete(t.Context(), files.DeleteCommand{TenantID: tenant, FileID: file.ID}); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Get(t.Context(), tenant, file.ID); !errors.Is(err, files.ErrNotFound) {
			t.Fatalf("deleted metadata: %v", err)
		}
		if err := service.Delete(t.Context(), files.DeleteCommand{TenantID: tenant, FileID: file.ID}); !errors.Is(err, files.ErrNotFound) {
			t.Fatalf("repeated delete: %v", err)
		}
		if objectExists(t, pool, oid) {
			t.Fatal("deletion orphaned the content")
		}
	}
}

func TestFilesRejectInvalidIdentifiers(t *testing.T) {
	store, service := open(t, pgtest.Open(t))
	tenant := uuid.NewString()
	file := create(t, service, t.Context(), tenant, []byte("x"))
	id := strings.TrimPrefix(file.ID, "file-")
	for _, missing := range []string{id, "file-" + strings.ToUpper(id), "file-" + uuid.Nil.String(), "invalid", ""} {
		if _, err := store.Get(t.Context(), tenant, missing); !errors.Is(err, files.ErrNotFound) {
			t.Fatalf("Get(%q) = %v", missing, err)
		}
	}
	for _, invalid := range []string{"", "invalid", uuid.Nil.String()} {
		if _, err := store.Get(t.Context(), invalid, file.ID); !errors.Is(err, files.ErrInvalidInput) {
			t.Fatalf("tenant %q: %v", invalid, err)
		}
		if _, err := service.Create(t.Context(), files.CreateCommand{TenantID: invalid, Upload: upload(nil)}); !errors.Is(err, files.ErrInvalidInput) {
			t.Fatalf("create in tenant %q: %v", invalid, err)
		}
	}
}

func auditContext(ctx context.Context, tenant, request string) context.Context {
	return writeaudit.WithSource(ctx, writeaudit.Source{
		KeyID: "static:" + strings.Repeat("a", 64), Name: "file audit fixture", Prefix: "aaaaaaaa",
		Kind: "static", TenantID: tenant, RequestID: request, TraceID: "file-audit-trace",
	})
}

// project registers tenant as a Project, which administrator audit rows
// reference.
func project(t *testing.T, pool *pgxpool.Pool, tenant string) {
	t.Helper()
	for _, statement := range []string{
		"INSERT INTO execution_project_scopes(tenant_id,organization_id,project_id) VALUES($1,'files',$2)",
		"INSERT INTO projects(id,name,tenant_id,subject_kind,subject_id) VALUES($1,'Files fixture',$2,'service_account','project:files')",
	} {
		if _, err := pool.Exec(t.Context(), statement, tenant, tenant); err != nil {
			t.Fatal(err)
		}
	}
}

func adminContext(ctx context.Context, tenant, request string) context.Context {
	// An inherited public provenance must not turn an administrator operation
	// into a user-key operation.
	return adminaudit.WithSource(auditContext(ctx, tenant, request), adminaudit.Source{
		CredentialID: "87654321", ActorLabel: "administrator fixture", ProjectID: tenant, RequestID: request, TraceID: "admin-trace",
	})
}

// Every failed create or delete leaves no row, content or audit behind. The
// database is isolated because it counts every large object and installs
// triggers that fail the final audit insertion.
func TestFilesRollBackFailedWrites(t *testing.T) {
	pool := pgtest.OpenIsolated(t, nil)
	_, err := pool.Exec(t.Context(), `CREATE FUNCTION reject_file_audit_fixture() RETURNS trigger LANGUAGE plpgsql AS $$
	BEGIN IF NEW.request_id = 'reject-file-audit' THEN RAISE EXCEPTION 'forced audit insertion failure'; END IF; RETURN NEW; END $$;
	CREATE TRIGGER reject_file_audit_fixture BEFORE INSERT ON write_audit_operations FOR EACH ROW EXECUTE FUNCTION reject_file_audit_fixture();
	CREATE TRIGGER reject_file_admin_audit_fixture BEFORE INSERT ON admin_audit_log FOR EACH ROW EXECUTE FUNCTION reject_file_audit_fixture()`)
	if err != nil {
		t.Fatal(err)
	}
	_, service := open(t, pool)
	tenant := uuid.NewString()
	project(t, pool, tenant)
	snapshot := func() string {
		t.Helper()
		var rows string
		if err := pool.QueryRow(t.Context(), `SELECT (SELECT COALESCE(jsonb_agg(to_jsonb(r) ORDER BY r.id)::text, '[]') FROM source_files r)
			|| (SELECT count(*) FROM write_audit_operations)::text || (SELECT count(*) FROM write_audit_owners)::text || (SELECT count(*) FROM admin_audit_log)::text`).Scan(&rows); err != nil {
			t.Fatal(err)
		}
		return rows
	}
	kept := create(t, service, t.Context(), tenant, []byte("kept"))
	before, objects := snapshot(), objectCount(t, pool)
	uploadFailure := errors.New("interrupted upload")
	for name, test := range map[string]struct {
		ctx    func(context.Context) context.Context
		upload func(cancel context.CancelFunc) func(io.Writer) (files.Upload, error)
		delete bool
		want   error
	}{
		"interrupted body": {upload: func(context.CancelFunc) func(io.Writer) (files.Upload, error) {
			return func(w io.Writer) (files.Upload, error) {
				_, _ = w.Write([]byte("not committed"))
				return files.Upload{}, uploadFailure
			}
		}, want: uploadFailure},
		"invalid purpose":  {upload: envelope(files.Upload{Filename: "source.bin", Purpose: "batch"}), want: files.ErrInvalidInput},
		"invalid filename": {upload: envelope(files.Upload{Filename: "bad\x00name", Purpose: files.PurposeUserData}), want: files.ErrInvalidInput},
		"cancelled": {upload: func(cancel context.CancelFunc) func(io.Writer) (files.Upload, error) {
			return func(w io.Writer) (files.Upload, error) {
				_, err := w.Write([]byte("not committed"))
				cancel()
				return files.Upload{Filename: "source.bin", Purpose: files.PurposeUserData}, err
			}
		}},
		"invalid audit source": {ctx: func(ctx context.Context) context.Context {
			return auditContext(ctx, uuid.NewString(), "foreign-source")
		}, upload: envelope(files.Upload{Filename: "source.bin", Purpose: files.PurposeUserData}), want: writeaudit.ErrInvalidSource},
		"create audit failure": {ctx: func(ctx context.Context) context.Context {
			return auditContext(ctx, tenant, "reject-file-audit")
		}, upload: envelope(files.Upload{Filename: "source.bin", Purpose: files.PurposeUserData})},
		"delete audit failure": {ctx: func(ctx context.Context) context.Context {
			return auditContext(ctx, tenant, "reject-file-audit")
		}, delete: true},
		"delete administrator audit failure": {ctx: func(ctx context.Context) context.Context {
			return adminContext(ctx, tenant, "reject-file-audit")
		}, delete: true},
	} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if test.ctx != nil {
				ctx = test.ctx(ctx)
			}
			if test.delete {
				err = service.Delete(ctx, files.DeleteCommand{TenantID: tenant, FileID: kept.ID})
			} else {
				_, err = service.Create(ctx, files.CreateCommand{TenantID: tenant, Upload: test.upload(cancel)})
			}
			if err == nil || test.want != nil && !errors.Is(err, test.want) {
				t.Fatalf("got %v, want %v", err, test.want)
			}
			if snapshot() != before || objectCount(t, pool) != objects {
				t.Fatal("failed write left rows, content or audit")
			}
		})
	}
}

func envelope(input files.Upload) func(context.CancelFunc) func(io.Writer) (files.Upload, error) {
	return func(context.CancelFunc) func(io.Writer) (files.Upload, error) {
		return func(w io.Writer) (files.Upload, error) {
			_, err := w.Write([]byte("audit-private-token"))
			return input, err
		}
	}
}

func TestFilesRecordWriteAudit(t *testing.T) {
	pool := pgtest.Open(t)
	_, service := open(t, pool)
	tenant := uuid.NewString()
	created := create(t, service, auditContext(t.Context(), tenant, "create-request"), tenant, []byte("audit-private-token"))
	if err := service.Delete(auditContext(t.Context(), tenant, "delete-request"), files.DeleteCommand{TenantID: tenant, FileID: created.ID}); err != nil {
		t.Fatal(err)
	}
	unattributed := create(t, service, t.Context(), tenant, []byte("unattributed"))
	var operations string
	if err := pool.QueryRow(t.Context(), `SELECT string_agg(action || ' ' || resource_type || ' ' || resource_id || ' ' || request_id, ',' ORDER BY request_id)
		FROM write_audit_operations WHERE tenant_id=$1`, tenant).Scan(&operations); err != nil {
		t.Fatal(err)
	}
	if want := "create file " + created.ID + " create-request,delete file " + created.ID + " delete-request"; operations != want {
		t.Fatalf("operations %q, want %q", operations, want)
	}
	var owners int
	var raw string
	if err := pool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM write_audit_owners WHERE tenant_id=$1 AND resource_type='file' AND resource_id=$2),
		(SELECT COALESCE(jsonb_agg(to_jsonb(r))::text, '') FROM write_audit_operations r WHERE tenant_id=$1)`, tenant, created.ID).Scan(&owners, &raw); err != nil {
		t.Fatal(err)
	}
	if owners != 1 || strings.Contains(raw, "audit-private-token") || strings.Contains(raw, unattributed.ID) {
		t.Fatal("create ownership or audit content differs", owners)
	}
}

func TestFilesDeleteRecordsAdministratorAudit(t *testing.T) {
	pool := pgtest.Open(t)
	store, service := open(t, pool)
	tenant := uuid.NewString()
	project(t, pool, tenant)
	file := create(t, service, t.Context(), tenant, []byte("audit-private-token"))
	oid := bodyOID(t, pool, tenant, file)
	if err := service.Delete(adminContext(t.Context(), tenant, "admin-request"), files.DeleteCommand{TenantID: tenant, FileID: file.ID}); err != nil {
		t.Fatal(err)
	}
	var credential, action, kind, id, raw string
	if err := pool.QueryRow(t.Context(), `SELECT admin_credential_id,action,resource_type,resource_id,to_jsonb(a)::text FROM admin_audit_log a WHERE tenant_id=$1 AND request_id='admin-request'`, tenant).Scan(&credential, &action, &kind, &id, &raw); err != nil {
		t.Fatal(err)
	}
	if credential != "87654321" || action != "delete" || kind != "file" || id != file.ID || strings.Contains(raw, "audit-private-token") {
		t.Fatal("administrator audit identity differs", raw)
	}
	var operations int
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM write_audit_operations WHERE tenant_id=$1", tenant).Scan(&operations); err != nil || operations != 0 {
		t.Fatal("administrator impersonated public-key provenance", operations, err)
	}
	if _, err := store.Get(t.Context(), tenant, file.ID); !errors.Is(err, files.ErrNotFound) || objectExists(t, pool, oid) {
		t.Fatal("administrator deletion left the File", err)
	}
}

func TestFileReadAdmittedBeforeDeletionCompletes(t *testing.T) {
	pool := pgtest.Open(t)
	store, service := open(t, pool)
	tenant := uuid.NewString()
	data := bytes.Repeat([]byte("immutable\x00"), 10000)
	file := create(t, service, t.Context(), tenant, data)
	if err := store.Read(t.Context(), tenant, file.ID, func(_ files.File, r io.Reader) error {
		prefix := make([]byte, 1)
		if _, err := io.ReadFull(r, prefix); err != nil {
			return err
		}
		_, other := open(t, pool)
		if err := other.Delete(t.Context(), files.DeleteCommand{TenantID: tenant, FileID: file.ID}); err != nil {
			return err
		}
		if _, err := store.Get(t.Context(), tenant, file.ID); !errors.Is(err, files.ErrNotFound) {
			t.Fatalf("new read after delete: %v", err)
		}
		rest, err := io.ReadAll(r)
		if !bytes.Equal(append(prefix, rest...), data) {
			t.Error("deletion damaged admitted read")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func TestFileReadReturnsConsumerErrors(t *testing.T) {
	store, service := open(t, pgtest.Open(t))
	tenant := uuid.NewString()
	file := create(t, service, t.Context(), tenant, []byte("content"))
	stop := errors.New("consumer stopped")
	if err := store.Read(t.Context(), tenant, file.ID, func(files.File, io.Reader) error { return stop }); err != stop {
		t.Fatalf("Read() = %v, want the consumer's error", err)
	}
}

func TestFileLargeStream(t *testing.T) {
	if os.Getenv("OAC_TEST_SOURCE_FILE_LARGE") != "1" {
		t.Skip("opt-in 512 MiB source storage acceptance")
	}
	store, service := open(t, pgtest.Open(t))
	tenant := uuid.NewString()
	chunk := bytes.Repeat([]byte("source\x00binary"), 20000)
	want := sha256.New()
	file, err := service.Create(t.Context(), files.CreateCommand{TenantID: tenant, Upload: func(w io.Writer) (files.Upload, error) {
		for left := files.MaxBytes; left > 0; {
			b := chunk[:min(int64(len(chunk)), left)]
			if _, err := w.Write(b); err != nil {
				return files.Upload{}, err
			}
			want.Write(b)
			left -= int64(len(b))
		}
		return files.Upload{Filename: "large.bin", Purpose: files.PurposeUserData}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := service.Delete(context.Background(), files.DeleteCommand{TenantID: tenant, FileID: file.ID}); err != nil {
			t.Error(err)
		}
	})
	got := sha256.New()
	if err := store.Read(t.Context(), tenant, file.ID, func(meta files.File, r io.Reader) error {
		n, err := io.CopyBuffer(got, r, chunk)
		if n != files.MaxBytes || meta.SizeBytes != n {
			t.Errorf("size: %d metadata: %d", n, meta.SizeBytes)
		}
		return err
	}); err != nil || !bytes.Equal(got.Sum(nil), want.Sum(nil)) {
		t.Fatalf("large stream mismatch: %v", err)
	}
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
