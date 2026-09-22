package store

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

// Deployment identity belongs to a whole database, so managed fixtures cannot
// share the ordinary Store fixture database or bypass production startup checks.
func newManagedTestStore(t *testing.T) (*Store, *pgxpool.Pool) {
	t.Helper()
	_, admin := testStore(t)
	name := "parsar_agents_api_m_" + uuid.NewString()[:8] + "_tests"
	quoted := pgx.Identifier{name}.Sanitize()
	if _, err := admin.Exec(t.Context(), "CREATE DATABASE "+quoted); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := admin.Exec(ctx, "DROP DATABASE "+quoted+" WITH (FORCE)"); err != nil {
			t.Error(err)
		}
	})
	cfg := admin.Config().Copy()
	cfg.ConnConfig.Database = name
	db := sql.OpenDB(stdlib.GetConnector(*cfg.ConnConfig))
	provider, err := goose.NewProvider(goose.DialectPostgres, db, os.DirFS("../../migrations"), goose.WithTableName("agents_api_schema_version"))
	if err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	_, err = provider.Up(t.Context())
	_ = db.Close()
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return New(pool), pool
}

func NewManagedTestStore(t *testing.T) (*Store, *pgxpool.Pool) {
	t.Helper()
	return newManagedTestStore(t)
}
