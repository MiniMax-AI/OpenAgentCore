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

// The deployment's saved Core address moves to each node it enrolled, and a
// rollback takes it back from them.
func TestPublicURLMigrationMovesTheAddressToNodes(t *testing.T) {
	_, pool := testStore(t)
	ctx := t.Context()
	schema := "public_url_" + uuid.NewString()[:8]
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
	if _, err := provider.UpTo(ctx, 74); err != nil {
		t.Fatal(err)
	}
	installation, active, removed := uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, err := db.ExecContext(ctx, `UPDATE runtime_deployment SET installation_id=$1, web_managed=true, provider_kind='docker', mode='nodes', generation=1,
		core_url='https://core.example', backend_fingerprint=$2`, installation, strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	for _, node := range []struct {
		id      string
		removed bool
	}{{active, false}, {removed, true}} {
		if _, err := db.ExecContext(ctx, `INSERT INTO runtime_nodes(id,installation_id,name,backend_fingerprint,credential_sha256,max_active,max_retained,removed_at)
			VALUES ($1,$2,'node',$3,$4,2,8,CASE WHEN $5 THEN clock_timestamp() END)`, node.id, installation, strings.Repeat("a", 64), strings.Repeat("c", 64), node.removed); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := provider.UpTo(ctx, 75); err != nil {
		t.Fatal(err)
	}
	address := func(id string) string {
		t.Helper()
		var value string
		if err := db.QueryRowContext(ctx, "SELECT core_url FROM runtime_nodes WHERE id=$1", id).Scan(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	if address(active) != "https://core.example" || address(removed) != "" {
		t.Fatal("migration did not record the enrolled address on the active node only")
	}
	if _, err := provider.DownTo(ctx, 74); err != nil {
		t.Fatal(err)
	}
	var restored string
	if err := db.QueryRowContext(ctx, "SELECT core_url FROM runtime_deployment").Scan(&restored); err != nil || restored != "https://core.example" {
		t.Fatal("rollback lost the deployment address", restored, err)
	}
}
