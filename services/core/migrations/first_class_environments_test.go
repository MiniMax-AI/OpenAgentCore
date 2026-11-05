package migrations_test

import (
	"context"
	"database/sql"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgtest"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/migrations"
)

// The Environment migration carries a populated database forward: the Session
// keeps its Environment, which takes over the Project, configuration and
// preparation input the Session held.
func TestFirstClassEnvironmentsMigrationKeepsPopulatedData(t *testing.T) {
	admin := pgtest.Open(t)
	name := "oac_migration_" + uuid.NewString()[:8] + "_tests"
	quoted := pgx.Identifier{name}.Sanitize()
	if _, err := admin.Exec(t.Context(), "CREATE DATABASE "+quoted); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { admin.Exec(context.Background(), "DROP DATABASE "+quoted+" WITH (FORCE)") })
	cfg := admin.Config().ConnConfig.Copy()
	cfg.Database = name
	dsn := stdlib.RegisterConnConfig(cfg)
	defer stdlib.UnregisterConnConfig(dsn)
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		t.Fatal(err)
	}
	// The same version table Apply uses, over the same migration files.
	provider, err := goose.NewProvider(goose.DialectPostgres, db, os.DirFS("."),
		goose.WithTableName("oac_schema_version"), goose.WithSessionLocker(locker))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.UpTo(t.Context(), 13); err != nil {
		t.Fatal(err)
	}

	tenant, session, environment := uuid.NewString(), uuid.NewString(), uuid.NewString()
	for _, statement := range []string{
		`INSERT INTO sessions(id, tenant_id, engine, idempotency_key, request_hash, configuration) VALUES ('` + session + `', '` + tenant + `', 'codex', 'key', 'hash', '{"environment":{"type":"openai_hosted"},"model":"m"}')`,
		`INSERT INTO environments(id, session_id) VALUES ('` + environment + `', '` + session + `')`,
		`INSERT INTO environment_setups(session_id, contents) VALUES ('` + session + `', '\x01')`,
		`INSERT INTO initial_environment_files(id, session_id, position, path, size_bytes, contents) VALUES ('` + uuid.NewString() + `', '` + session + `', 0, 'a.txt', 1, '\x02')`,
	} {
		if _, err := db.ExecContext(t.Context(), statement); err != nil {
			t.Fatal(err, statement)
		}
	}
	if err := migrations.Apply(t.Context(), dsn); err != nil {
		t.Fatal(err)
	}

	var attached, kind string
	var sessionOwned bool
	var stored string
	if err := db.QueryRowContext(t.Context(), `SELECT s.environment_id::text, e.tenant_id::text || (e.configuration->>'type'), e.session_owned, s.configuration::text
		FROM sessions s JOIN environments e ON e.id = s.environment_id WHERE s.id = $1`, session).Scan(&attached, &kind, &sessionOwned, &stored); err != nil {
		t.Fatal(err)
	}
	if attached != environment || kind != tenant+"openai_hosted" || !sessionOwned || stored != `{"model": "m"}` {
		t.Fatal(attached, kind, sessionOwned, stored)
	}
	var setups, files int
	if err := db.QueryRowContext(t.Context(), `SELECT (SELECT count(*) FROM environment_setups WHERE environment_id = $1), (SELECT count(*) FROM initial_environment_files WHERE environment_id = $1)`, environment).Scan(&setups, &files); err != nil {
		t.Fatal(err)
	}
	if setups != 1 || files != 1 {
		t.Fatal("preparation input did not follow the Environment", setups, files)
	}
}
