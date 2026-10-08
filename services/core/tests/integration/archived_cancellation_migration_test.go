package integration

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestArchivedCancellationMigrationDoesNotAdoptOldRevocations(t *testing.T) {
	db, provider := runtimeNamesMigrationSchema(t)
	ctx := t.Context()
	if _, err := provider.UpTo(ctx, 79); err != nil {
		t.Fatal(err)
	}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	tenant, session, environment, device, allocation, turn := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	exec(`INSERT INTO sessions(id,tenant_id,engine,idempotency_key,request_hash,configuration) VALUES($1,$2,'codex','archive','archive','{}')`, session, tenant)
	exec(`INSERT INTO environments(id,session_id) VALUES($1,$2)`, environment, session)
	exec(`INSERT INTO devices(id,tenant_id,name,credential_hash,revoked_at) VALUES($1,$2,'revoked',repeat('a',64),clock_timestamp())`, device, tenant)
	exec(`INSERT INTO runtime_allocations(id,environment_id,device_id,provider_key) VALUES($1,$2,$3,$4)`, allocation, environment, device, uuid.NewString())
	exec(`INSERT INTO turns(id,session_id,status) VALUES($1,$2,'in_progress')`, turn, session)
	if _, err := provider.UpTo(ctx, 80); err != nil {
		t.Fatal(err)
	}
	var marker *string
	if err := db.QueryRowContext(ctx, "SELECT archive_cancel_turn_id::text FROM devices WHERE id=$1", device).Scan(&marker); err != nil || marker != nil {
		t.Fatal("upgrade adopted historical revoked device", marker, err)
	}
	exec(`UPDATE devices SET archive_cancel_turn_id=$2 WHERE id=$1`, device, turn)
	if _, err := provider.DownTo(ctx, 79); err == nil || !strings.Contains(err.Error(), "Cannot remove archived cancellation receipts while cleanup is unsettled") {
		t.Fatal("downgrade discarded unsettled cancellation marker", err)
	}
	exec(`UPDATE runtime_allocations SET state='released',released_at=clock_timestamp(),create_settled=true WHERE id=$1`, allocation)
	if _, err := provider.DownTo(ctx, 79); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.UpTo(ctx, 80); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, "SELECT archive_cancel_turn_id::text FROM devices WHERE id=$1", device).Scan(&marker); err != nil || marker != nil {
		t.Fatal("re-upgrade adopted historical revoked device", marker, err)
	}
}
