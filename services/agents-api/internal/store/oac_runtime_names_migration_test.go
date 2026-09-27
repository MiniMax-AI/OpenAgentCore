package store

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

func runtimeNamesMigrationSchema(t *testing.T) (*sql.DB, *goose.Provider) {
	t.Helper()
	_, pool := testStore(t)
	schema := "runtime_names_" + uuid.NewString()[:8]
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err := pool.Exec(t.Context(), "CREATE SCHEMA "+quoted); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
			t.Error(err)
		}
	})
	cfg := pool.Config().ConnConfig.Copy()
	cfg.RuntimeParams["search_path"] = schema
	db := sql.OpenDB(stdlib.GetConnector(*cfg))
	t.Cleanup(func() { _ = db.Close() })
	provider, err := goose.NewProvider(goose.DialectPostgres, db, os.DirFS("../../migrations"), goose.WithTableName("agents_api_schema_version"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = provider.UpTo(t.Context(), 77); err != nil {
		t.Fatal(err)
	}
	return db, provider
}

func TestRuntimeNamesMigrationRequiresReleasedAllocationsBothDirections(t *testing.T) {
	db, provider := runtimeNamesMigrationSchema(t)
	ctx := t.Context()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	read := func(query string) string {
		t.Helper()
		var value string
		if err := db.QueryRowContext(ctx, query).Scan(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	digest := strings.Repeat("a", 64)
	oldRef, newRef := "parsar-core-runtime@sha256:"+digest, "oac-runtime@sha256:"+digest
	specification := `{"runtime":{"microsandbox_ref":"` + oldRef + `","image_id":"sha256:` + digest + `"},"resources":{"cpus":2}}`
	exec("UPDATE runtime_deployment SET specification=$1", specification)
	session, tenant, environment, device, allocation := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	exec(`INSERT INTO sessions(id,tenant_id,engine,idempotency_key,request_hash,configuration) VALUES ($1,$2,'codex','old','old','{"history":"parsar_worker /home/runtime/.parsar"}')`, session, tenant)
	exec("INSERT INTO environments(id,session_id) VALUES ($1,$2)", environment, session)
	exec("INSERT INTO devices(id,tenant_id,name,credential_hash) VALUES ($1,$2,'old',$3)", device, tenant, digest)
	exec("INSERT INTO runtime_allocations(id,environment_id,device_id,provider_key) VALUES ($1,$2,$3,$4)", allocation, environment, device, uuid.NewString())
	history := read("SELECT to_jsonb(s)::text FROM sessions s")
	for _, direction := range []string{"up", "down"} {
		before := read("SELECT specification::text FROM runtime_deployment")
		for _, state := range []string{"creating", "running", "cleanup_pending", "suspended"} {
			phase, stored := "disabled", state
			if state == "suspended" {
				phase, stored = "suspended", "running"
			}
			exec("UPDATE runtime_allocations SET state=$1,released_at=NULL,create_settled=true,compute_phase=$2", stored, phase)
			allocationBefore := read("SELECT to_jsonb(a)::text FROM runtime_allocations a")
			var err error
			if direction == "up" {
				_, err = provider.UpTo(ctx, 78)
			} else {
				_, err = provider.DownTo(ctx, 77)
			}
			if err == nil || !strings.Contains(err.Error(), "Drain the sandbox deployment") {
				t.Fatalf("%s with %s allocation did not refuse: %v", direction, state, err)
			}
			if got := read("SELECT specification::text FROM runtime_deployment"); got != before {
				t.Fatal("refused migration changed specification")
			}
			if got := read("SELECT to_jsonb(a)::text FROM runtime_allocations a"); got != allocationBefore {
				t.Fatal("refused migration changed allocation")
			}
		}
		exec("UPDATE runtime_allocations SET state='released',released_at=clock_timestamp(),create_settled=true")
		released := read("SELECT to_jsonb(a)::text FROM runtime_allocations a")
		var err error
		expected := strings.Replace(before, oldRef, newRef, 1)
		if direction == "up" {
			_, err = provider.UpTo(ctx, 78)
		} else {
			_, err = provider.DownTo(ctx, 77)
			expected = strings.Replace(before, newRef, oldRef, 1)
		}
		if err != nil {
			t.Fatal("released allocation blocked migration", err)
		}
		if got := read("SELECT specification::text FROM runtime_deployment"); got != expected {
			t.Fatalf("migration changed fields beyond reference: %s", got)
		}
		if got := read("SELECT to_jsonb(a)::text FROM runtime_allocations a"); got != released {
			t.Fatal("migration changed released allocation")
		}
		if got := read("SELECT to_jsonb(s)::text FROM sessions s"); got != history {
			t.Fatal("migration rewrote historical Session content")
		}
	}
}

func TestRuntimeNamesMigrationPreservesAbsentAndOtherReferences(t *testing.T) {
	db, provider := runtimeNamesMigrationSchema(t)
	for _, spec := range []string{`{}`, `{"runtime":null}`, `{"runtime":{"microsandbox_ref":"custom-runtime@sha256:` + strings.Repeat("b", 64) + `"}}`} {
		if _, err := db.ExecContext(t.Context(), "UPDATE runtime_deployment SET specification=$1", spec); err != nil {
			t.Fatal(err)
		}
		for _, up := range []bool{true, false} {
			var err error
			if up {
				_, err = provider.UpTo(t.Context(), 78)
			} else {
				_, err = provider.DownTo(t.Context(), 77)
			}
			if err != nil {
				t.Fatal(err)
			}
			var equal bool
			if err = db.QueryRowContext(t.Context(), "SELECT specification=$1::jsonb FROM runtime_deployment", spec).Scan(&equal); err != nil || !equal {
				t.Fatal("migration changed unrelated reference", err)
			}
		}
	}
}
