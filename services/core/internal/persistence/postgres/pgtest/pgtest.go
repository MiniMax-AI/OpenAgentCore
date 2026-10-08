// Package pgtest opens PostgreSQL databases for Core tests. Only test files
// import it.
package pgtest

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/migrations"
)

// CredentialKey returns a cipher under a fixed test credential key, for tests
// that need a key but not a particular one.
func CredentialKey(t testing.TB) *credentialcrypto.Cipher {
	t.Helper()
	cipher, err := credentialcrypto.New(bytes.Repeat([]byte{0x7e}, 32))
	if err != nil {
		t.Fatal(err)
	}
	return cipher
}

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
	if err := copyTemplate(t.Context(), admin, quoted); err != nil {
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

// template is the database OpenIsolated copies: oac_*_template_tests beside
// the shared test database. It persists across runs, and each test process
// migrates it once, as Open migrates the shared database.
var template struct {
	once sync.Once
	name string
	err  error
}

// copyTemplate creates the quoted database from the migrated template.
// Postgres refuses to copy a template while another session is connected to
// it, so migrating and copying hold one advisory lock across test processes.
func copyTemplate(ctx context.Context, admin *pgxpool.Pool, quoted string) error {
	conn, err := admin.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	if _, err = conn.Exec(ctx, "SELECT pg_advisory_lock(hashtext('oac_test_template'))"); err != nil {
		return err
	}
	defer conn.Exec(context.Background(), "SELECT pg_advisory_unlock(hashtext('oac_test_template'))")
	template.once.Do(func() {
		cfg := admin.Config().Copy()
		cfg.ConnConfig.Database = strings.TrimSuffix(cfg.ConnConfig.Database, "_tests") + "_template_tests"
		_, err := conn.Exec(context.Background(), "CREATE DATABASE "+pgx.Identifier{cfg.ConnConfig.Database}.Sanitize())
		var exists *pgconn.PgError
		if err != nil && (!errors.As(err, &exists) || exists.Code != "42P04") {
			template.err = err
			return
		}
		connection := stdlib.RegisterConnConfig(cfg.ConnConfig)
		defer stdlib.UnregisterConnConfig(connection)
		template.name, template.err = cfg.ConnConfig.Database, migrations.Apply(context.Background(), connection)
	})
	if template.err != nil {
		return template.err
	}
	_, err = conn.Exec(ctx, "CREATE DATABASE "+quoted+" TEMPLATE "+pgx.Identifier{template.name}.Sanitize())
	return err
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
