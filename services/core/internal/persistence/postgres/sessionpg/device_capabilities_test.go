package sessionpg

import (
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgtest"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestAuthenticatedCapabilitiesKeepRevokedReceipt(t *testing.T) {
	pool := pgtest.OpenIsolated(t, nil)
	tenant, _, _ := newEnvironment(t, pool, "self_hosted", "pending")
	device := newDevice(t, pool, tenant, pgtype.UUID{})
	store := New(pgunit.NewPool(pool), nil)
	kinds := []runtimedevice.SupportedAgentKind{{Kind: "fixture", Available: true, Capabilities: runtimedevice.KindCapabilities{RetainedNativeHistory: true}}}
	correct := strings.Repeat("a", 64)
	if ok, err := store.TouchAuthenticatedDevice(t.Context(), device, correct, kinds); err != nil || !ok {
		t.Fatal(ok, err)
	}
	read := func() string {
		t.Helper()
		var raw string
		if err := pool.QueryRow(t.Context(), `SELECT supported_agent_kinds::text FROM devices WHERE id=$1`, device).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		return raw
	}
	before := read()
	if ok, err := store.TouchAuthenticatedDevice(t.Context(), device, strings.Repeat("b", 64), nil); err != nil || ok || read() != before {
		t.Fatal("wrong credential changed receipt", ok, err)
	}
	if ok, err := store.TouchAuthenticatedDevice(t.Context(), device, correct, nil); err != nil || !ok || read() != "[]" {
		t.Fatal("unknown capabilities not cleared", ok, err)
	}
	if _, err := store.TouchAuthenticatedDevice(t.Context(), device, correct, kinds); err != nil {
		t.Fatal(err)
	}
	before = read()
	exec(t, pool, `UPDATE devices SET revoked_at=clock_timestamp() WHERE id=$1`, device)
	if ok, err := store.TouchAuthenticatedDevice(t.Context(), device, correct, nil); err != nil || ok || read() != before {
		t.Fatal("revoked device changed retained receipt", ok, err)
	}
}
