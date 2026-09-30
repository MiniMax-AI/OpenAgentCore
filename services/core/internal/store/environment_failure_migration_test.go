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

// The failure columns are additive for existing Environments, and a rollback
// refuses to discard a recorded hosted provisioning failure.
func TestEnvironmentFailureMigrationGuardsRecordedFailures(t *testing.T) {
	_, pool := testStore(t)
	ctx := t.Context()
	schema := "environment_failure_" + uuid.NewString()[:8]
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
	if _, err := provider.UpTo(ctx, 61); err != nil {
		t.Fatal(err)
	}
	session, environment := uuid.NewString(), uuid.NewString()
	if _, err := db.ExecContext(ctx, `INSERT INTO sessions(id,tenant_id,engine,idempotency_key,request_hash,configuration)
		VALUES ($1,$2,'codex','historical','historical','{"environment":{"type":"openai_hosted"}}')`, session, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	// A failure recorded before this migration keeps no reason.
	if _, err := db.ExecContext(ctx, "INSERT INTO environments(id,session_id,status) VALUES ($1,$2,'failed')", environment, session); err != nil {
		t.Fatal(err)
	}
	upgrade := func() {
		t.Helper()
		if _, err := provider.UpTo(ctx, 62); err != nil {
			t.Fatal(err)
		}
		var reason, failedAt sql.NullString
		if err := db.QueryRowContext(ctx, "SELECT failure_reason, failed_at::text FROM environments WHERE id=$1", environment).Scan(&reason, &failedAt); err != nil || reason.Valid || failedAt.Valid {
			t.Fatal("migration invented a failure reason", reason, failedAt, err)
		}
	}
	upgrade()
	for _, statement := range []string{
		"UPDATE environments SET failure_reason='reason' WHERE id=$1",
		"UPDATE environments SET status='connected', failure_reason='reason', failed_at=clock_timestamp() WHERE id=$1",
		"UPDATE environments SET failure_reason=repeat('x', 257), failed_at=clock_timestamp() WHERE id=$1",
	} {
		if _, err := db.ExecContext(ctx, statement, environment); err == nil || !strings.Contains(err.Error(), "environment_failure_recorded") {
			t.Fatal("inconsistent failure accepted", statement, err)
		}
	}
	if _, err := provider.DownTo(ctx, 61); err != nil {
		t.Fatal("rollback without recorded failures failed", err)
	}
	upgrade()
	if _, err := db.ExecContext(ctx, "UPDATE environments SET failure_reason='Failed to provision environment', failed_at=clock_timestamp() WHERE id=$1", environment); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.DownTo(ctx, 61); err == nil || !strings.Contains(err.Error(), "Cannot remove recorded hosted provisioning failures") {
		t.Fatal("rollback discarded a recorded failure", err)
	}
	var reason string
	if err := db.QueryRowContext(ctx, "SELECT failure_reason FROM environments WHERE id=$1", environment).Scan(&reason); err != nil || reason != "Failed to provision environment" {
		t.Fatal("refused rollback changed the failure", reason, err)
	}
}
