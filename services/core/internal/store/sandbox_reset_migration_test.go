package store

import (
	"strings"
	"testing"
)

func TestSandboxResetMigrationResumesAdmissionAndGuardsDowngrade(t *testing.T) {
	db, provider := runtimeNamesMigrationSchema(t)
	ctx := t.Context()
	if _, err := provider.UpTo(ctx, 78); err != nil {
		t.Fatal(err)
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	for _, paused := range []bool{false, true} {
		exec(`UPDATE runtime_deployment SET web_managed=true,provider_kind='docker',mode='nodes',generation=1,maintenance=$1`, paused)
		if _, err := provider.UpTo(ctx, 79); err != nil {
			t.Fatal(err)
		}
		var resumed bool
		if err := db.QueryRowContext(ctx, `SELECT NOT admission_paused AND reset_clear IS NULL AND generation=1 FROM runtime_deployment`).Scan(&resumed); err != nil || !resumed {
			t.Fatal("migration invented a reset or kept maintenance", err)
		}
		if _, err := provider.DownTo(ctx, 78); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := provider.UpTo(ctx, 79); err != nil {
		t.Fatal(err)
	}
	exec(`UPDATE runtime_deployment SET admission_paused=true,reset_clear='auto',reset_requested_at=clock_timestamp(),reset_deadline_at=clock_timestamp()+interval '1 hour',reset_audit='{}'`)
	if _, err := provider.DownTo(ctx, 78); err == nil || !strings.Contains(err.Error(), "reset is active") {
		t.Fatal("active reset downgrade", err)
	}
	exec(`UPDATE runtime_deployment SET admission_paused=false,reset_clear=NULL,reset_requested_at=NULL,reset_deadline_at=NULL,reset_audit=NULL,provider_kind='',mode='',generation=2`)
	if _, err := provider.DownTo(ctx, 78); err == nil || !strings.Contains(err.Error(), "Configure a sandbox provider") {
		t.Fatal("completed reset lost generation", err)
	}
	var generation int64
	if err := db.QueryRowContext(ctx, `SELECT generation FROM runtime_deployment`).Scan(&generation); err != nil || generation != 2 {
		t.Fatal(generation, err)
	}
	exec(`UPDATE runtime_deployment SET provider_kind='docker',mode='nodes',generation=3`)
	if _, err := provider.DownTo(ctx, 78); err != nil {
		t.Fatal("configured downgrade", err)
	}
}
