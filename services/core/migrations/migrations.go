// Package migrations owns only the Agents API database schema.
package migrations

import (
	"context"
	"database/sql"
	"embed"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

//go:embed *.sql
var files embed.FS

// Apply upgrades the dedicated execution database without consulting product
// data. A session advisory lock serializes concurrent Core starts.
func Apply(ctx context.Context, databaseURL string) error {
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return err
	}
	defer db.Close()
	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return err
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, db, files,
		goose.WithTableName("agents_api_schema_version"), goose.WithSessionLocker(locker))
	if err != nil {
		return err
	}
	_, err = provider.Up(ctx)
	return err
}
