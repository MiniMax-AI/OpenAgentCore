package store

import (
	"context"
	"database/sql"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

func TestSkillDefaultMetadataMigrationRepairsOnlyResourceMetadata(t *testing.T) {
	_, pool := testStore(t)
	ctx := t.Context()
	schema := "skill_metadata_" + uuid.NewString()[:8]
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err := pool.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
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
	if _, err := provider.UpTo(ctx, 54); err != nil {
		t.Fatal(err)
	}
	tenant, foreign := uuid.NewString(), uuid.NewString()
	bad, valid := uuid.NewString(), uuid.NewString()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for _, fixture := range []struct {
		id, tenant     string
		defaultVersion int
	}{{bad, tenant, 2}, {valid, foreign, 1}} {
		if _, err := tx.ExecContext(ctx, `INSERT INTO skills(id,tenant_id,name,description,default_version,latest_version,next_version)
			VALUES ($1,$2,'first','First description.', $3,2,3)`, fixture.id, fixture.tenant, fixture.defaultVersion); err != nil {
			t.Fatal(err)
		}
		for i, name := range []string{"first", "second"} {
			description := []string{"First description.", "Second description."}[i]
			if _, err := tx.ExecContext(ctx, `INSERT INTO skill_versions(id,tenant_id,skill_id,version,name,description,contents)
				VALUES ($1,$2,$3,$4,$5,$6,$7)`, uuid.NewString(), fixture.tenant, fixture.id, i+1, name, description, []byte{73, byte(i + 1), 29}); err != nil {
				t.Fatal(err)
			}
		}
	}
	session := uuid.NewString()
	if _, err := tx.ExecContext(ctx, `INSERT INTO sessions(id,tenant_id,engine,idempotency_key,request_hash,configuration)
		VALUES ($1,$2,'codex','historical','historical',jsonb_set('{"environment":{"skills":[{"type":"skill_reference","version":"1","name":"first","description":"First description."}]}}', '{environment,skills,0,skill_id}', to_jsonb($3::text)))`, session, tenant, "skill_"+bad); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO environment_setups(session_id,contents) VALUES ($1,$2)", session, []byte{94, 17, 28}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	snapshot := func(query string) string {
		t.Helper()
		var result string
		if err := db.QueryRowContext(ctx, query).Scan(&result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	const preserved = `SELECT jsonb_build_object(
		'skills', (SELECT jsonb_agg(to_jsonb(s)-'name'-'description' ORDER BY id) FROM skills s),
		'versions', (SELECT jsonb_agg(to_jsonb(v) ORDER BY id) FROM skill_versions v),
		'sessions', (SELECT jsonb_agg(to_jsonb(s) ORDER BY id) FROM sessions s),
		'setups', (SELECT jsonb_agg(to_jsonb(e) ORDER BY session_id) FROM environment_setups e))::text`
	before := snapshot(preserved)
	assertMetadata := func() {
		t.Helper()
		for _, fixture := range []struct{ id, name, description string }{
			{bad, "second", "Second description."}, {valid, "first", "First description."},
		} {
			var name, description string
			if err := db.QueryRowContext(ctx, "SELECT name,description FROM skills WHERE id=$1", fixture.id).Scan(&name, &description); err != nil || name != fixture.name || description != fixture.description {
				t.Fatal("incorrect repaired/control metadata", name, description, err)
			}
		}
		if snapshot(preserved) != before {
			t.Fatal("migration changed pointers, immutable versions or frozen Session data")
		}
	}
	if _, err := provider.UpTo(ctx, 55); err != nil {
		t.Fatal(err)
	}
	assertMetadata()
	const skillRows = `SELECT jsonb_agg(to_jsonb(s) || jsonb_build_object('row_version', xmin::text) ORDER BY id)::text FROM skills s`
	repaired := snapshot(skillRows)
	if _, err := provider.DownTo(ctx, 54); err != nil {
		t.Fatal(err)
	}
	assertMetadata()
	if _, err := provider.UpTo(ctx, 55); err != nil {
		t.Fatal(err)
	}
	assertMetadata()
	if snapshot(skillRows) != repaired {
		t.Fatal("reapplying data repair rewrote already correct rows")
	}
}
