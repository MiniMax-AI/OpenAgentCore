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

func TestOAuthMigrationPreservesStaticGrantsAndRefusesLossyRollback(t *testing.T) {
	_, pool := testStore(t)
	schema := "oauth_migration_" + uuid.NewString()[:8]
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
	if _, err := provider.UpTo(t.Context(), 53); err != nil {
		t.Fatal(err)
	}
	vault, id := uuid.NewString(), uuid.NewString()
	if _, err := db.ExecContext(t.Context(), "INSERT INTO vaults(id,tenant_id,metadata) VALUES ($1,$2,'{}')", vault, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), "INSERT INTO vault_credentials(id,vault_id,name,auth_type,mcp_server_url,token_ciphertext) VALUES ($1,$2,'Existing','static_bearer','https://mcp.example/tools',$3)", id, vault, []byte{1, 2, 3}); err != nil {
		t.Fatal(err)
	}
	snapshot := func() string {
		t.Helper()
		var value string
		if err := db.QueryRowContext(t.Context(), "SELECT (to_jsonb(c)-'oauth_metadata')::text FROM vault_credentials c WHERE id=$1", id).Scan(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	before := snapshot()
	if _, err := provider.UpTo(t.Context(), 54); err != nil || snapshot() != before {
		t.Fatal("OAuth migration changed static grant", err)
	}
	if _, err := provider.DownTo(t.Context(), 53); err != nil || snapshot() != before {
		t.Fatal("static-only rollback failed", err)
	}
	if _, err := provider.UpTo(t.Context(), 54); err != nil {
		t.Fatal(err)
	}
	oauthID := uuid.NewString()
	if _, err := db.ExecContext(t.Context(), "INSERT INTO vault_credentials(id,vault_id,name,auth_type,mcp_server_url,token_ciphertext,oauth_metadata) VALUES ($1,$2,'OAuth','mcp_oauth','https://mcp.example/tools',$3,'{}')", oauthID, vault, []byte{4, 5, 6}); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.DownTo(t.Context(), 53); err == nil {
		t.Fatal("rollback discarded OAuth support with existing grants")
	}
	var count int
	if err := db.QueryRowContext(t.Context(), "SELECT count(*) FROM vault_credentials WHERE id=$1 AND auth_type='mcp_oauth' AND oauth_metadata='{}'::jsonb", oauthID).Scan(&count); err != nil || count != 1 || snapshot() != before {
		t.Fatal("failed rollback altered grants", err)
	}
	for _, mutation := range []string{"oauth_metadata=NULL", "oauth_metadata='[]'::jsonb", "auth_type='static_bearer'"} {
		if _, err := db.ExecContext(t.Context(), "UPDATE vault_credentials SET "+mutation+" WHERE id=$1", oauthID); err == nil {
			t.Fatal("invalid OAuth metadata constraint admitted")
		}
	}
}
