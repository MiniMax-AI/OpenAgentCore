// Package migrations owns only the Agents API database schema.
package migrations

import (
	"context"
	"database/sql"
	"embed"
	"fmt"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

//go:embed *.sql
var files embed.FS

// Apply upgrades the dedicated execution database without consulting product
// data and refuses a schema newer than its migrations. A session advisory lock
// serializes concurrent Core starts.
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
	if _, err = provider.Up(ctx); err != nil {
		return err
	}
	// Up ignores versions above the embedded ones, so an older Core would start
	// on a schema it does not know.
	current, target, err := provider.GetVersions(ctx)
	if err != nil {
		return err
	}
	if current > target {
		return fmt.Errorf("Agents API database schema version %d is newer than this Core supports (%d). Downgrades are not supported: run the release that upgraded this database or a newer one, or restore the backup taken before the upgrade.", current, target)
	}
	return nil
}
