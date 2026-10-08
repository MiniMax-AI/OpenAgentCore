package integration

import (
	"database/sql"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

// TestAgentHostMigrationRefusesGuestBoundSessions upgrades a database where a
// Session is bound to a Runtime that is not an agent host: the upgrade refuses
// until that Session is deleted.
func TestAgentHostMigrationRefusesGuestBoundSessions(t *testing.T) {
	s, pool := newManagedTestStore(t)
	ctx := t.Context()
	tenant, session := newTurnSession(t, s)
	guest, _ := registerTestDevice(t, s, tenant)
	if _, err := pool.Exec(ctx, `INSERT INTO session_runtime_assignments (session_id, runtime_id) VALUES ($1, $2)`, session.ID, guest.ID); err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(stdlib.GetConnector(*pool.Config().ConnConfig))
	t.Cleanup(func() { _ = db.Close() })
	provider, err := goose.NewProvider(goose.DialectPostgres, db, os.DirFS("../../migrations"), goose.WithTableName("agents_api_schema_version"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.DownTo(ctx, 97); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Up(ctx); err == nil || !strings.Contains(err.Error(), "delete those Sessions, then upgrade") {
		t.Fatal("the upgrade kept a Session bound to a guest Runtime", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE sessions SET deleted_at = clock_timestamp() WHERE id = $1`, session.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Up(ctx); err != nil {
		t.Fatal(err)
	}
}
