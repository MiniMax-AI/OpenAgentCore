package integration

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

// A process-configured deployment refuses the upgrade; a Web-managed one
// keeps its installation and reset in both directions.
func TestWebDeploymentOnlyMigrationRefusesProcessDeployment(t *testing.T) {
	db, provider := runtimeNamesMigrationSchema(t)
	ctx := t.Context()
	if _, err := provider.UpTo(ctx, 92); err != nil {
		t.Fatal(err)
	}
	installation := uuid.NewString()
	if _, err := db.ExecContext(ctx, `UPDATE runtime_deployment SET installation_id=$1, backend_fingerprint=$2`, installation, strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.UpTo(ctx, 93); err == nil || !strings.Contains(err.Error(), "configured outside Web setup") {
		t.Fatal("process deployment upgraded", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE runtime_deployment SET web_managed=true, provider_kind='docker', mode='nodes', generation=1,
		admission_paused=true, reset_clear='force', reset_requested_at=now(), reset_forced_at=now(), reset_audit='{}'`); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.UpTo(ctx, 93); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.DownTo(ctx, 92); err != nil {
		t.Fatal(err)
	}
	var restored bool
	if err := db.QueryRowContext(ctx, `SELECT web_managed AND local_node_id IS NULL AND admission_paused AND installation_id=$1 FROM runtime_deployment`, installation).Scan(&restored); err != nil || !restored {
		t.Fatal("downgrade lost the Web installation", err)
	}
}
