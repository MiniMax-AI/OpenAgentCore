// Package pgtest opens PostgreSQL databases for Core tests. Only test files
// import it.
package pgtest

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/migrations"
)

// Open returns a pool on the dedicated test database named by
// OAC_TEST_DATABASE_URL, with Core's migrations applied, and skips the test when
// the variable is unset. Tests on this shared database isolate their data with
// fresh tenant and project IDs.
func Open(t testing.TB) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("OAC_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("OAC_TEST_DATABASE_URL is not set; dedicated PostgreSQL required")
	}
	cfg, err := databaseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	// A separate database, not product fixtures or migrations, is sufficient.
	var database string
	var productTable *string
	if err := pool.QueryRow(context.Background(), "SELECT current_database(), to_regclass('workspaces')::text").Scan(&database, &productTable); err != nil || productTable != nil || database != cfg.ConnConfig.Database {
		t.Fatal("execution tests require a database without product workspace tables")
	}
	if err := migrations.Apply(context.Background(), dsn); err != nil {
		t.Fatal(err)
	}
	return pool
}

// OpenIsolated creates a fresh migrated database beside the one Open uses and
// drops it when the test ends. Tests use it for state that belongs to a whole
// database, such as the execution lease or the sandbox deployment identity.
// configure, when not nil, adjusts the returned pool's configuration.
func OpenIsolated(t testing.TB, configure func(*pgxpool.Config)) *pgxpool.Pool {
	t.Helper()
	admin := Open(t)
	name := "oac_isolated_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12] + "_tests"
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
	connection := stdlib.RegisterConnConfig(cfg.ConnConfig)
	err := migrations.Apply(t.Context(), connection)
	stdlib.UnregisterConnConfig(connection)
	if err != nil {
		t.Fatal(err)
	}
	if configure != nil {
		configure(cfg)
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// databaseConfig validates the driver's effective database, so query
// parameters and key/value DSNs cannot redirect tests to another database.
func databaseConfig(dsn string) (*pgxpool.Config, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, errors.New("invalid test database configuration")
	}
	database := cfg.ConnConfig.Database
	if !strings.HasPrefix(database, "oac_") || !strings.HasSuffix(database, "_tests") {
		return nil, errors.New("test database must be named oac_*_tests")
	}
	return cfg, nil
}
