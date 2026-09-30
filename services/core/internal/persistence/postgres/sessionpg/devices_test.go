package sessionpg

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgtest"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

// newDevice stores a device of tenant, dedicated to environment when it is
// valid, and returns its ID.
func newDevice(t *testing.T, pool *pgxpool.Pool, tenant, environment pgtype.UUID) string {
	t.Helper()
	id := uuid.NewString()
	exec(t, pool, `INSERT INTO devices(id, tenant_id, name, credential_hash, environment_id) VALUES ($1, $2, 'runtime', $3, $4)`, id, tenant, strings.Repeat("a", 64), environment)
	return id
}

func TestBindSessionDeviceTranslatesTheBindingOutcome(t *testing.T) {
	pool := pgtest.OpenIsolated(t, nil)
	lease, err := pgunit.AcquireLease(t.Context(), pool)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lease.Close(context.Background()) })
	operations, err := sessions.NewExecutionOperations(NewExecution(lease))
	if err != nil {
		t.Fatal(err)
	}
	tenantID, sessionID, _ := newEnvironment(t, pool, "self_hosted", "pending")
	tenant, session := uuidText(tenantID), uuidText(sessionID)
	_, _, otherEnvironment := newEnvironment(t, pool, "self_hosted", "pending")
	otherTenant, _, _ := newEnvironment(t, pool, "self_hosted", "pending")
	device, replacement := newDevice(t, pool, tenantID, pgtype.UUID{}), newDevice(t, pool, tenantID, pgtype.UUID{})
	revoked := newDevice(t, pool, tenantID, pgtype.UUID{})
	exec(t, pool, `UPDATE devices SET revoked_at = clock_timestamp() WHERE id = $1`, revoked)

	for name, test := range map[string]struct {
		device string
		want   error
	}{
		"another tenant's device":       {newDevice(t, pool, otherTenant, pgtype.UUID{}), sessions.ErrNotFound},
		"revoked device":                {revoked, sessions.ErrNotFound},
		"device of another Environment": {newDevice(t, pool, tenantID, otherEnvironment), sessions.ErrDeviceBindingConflict},
		"malformed device":              {"device", sessions.ErrInvalidInput},
	} {
		if err := operations.BindSessionDevice(t.Context(), tenant, session, test.device); !errors.Is(err, test.want) {
			t.Fatalf("%s: %v, want %v", name, err, test.want)
		}
	}
	for range 2 {
		if err := operations.BindSessionDevice(t.Context(), tenant, session, device); err != nil {
			t.Fatal("binding the bound device again", err)
		}
	}
	if err := operations.BindSessionDevice(t.Context(), tenant, session, replacement); !errors.Is(err, sessions.ErrDeviceBindingConflict) {
		t.Fatal("rebinding the Session to another device", err)
	}
	var bound string
	if err := pool.QueryRow(t.Context(), `SELECT device_id::text FROM session_devices WHERE session_id = $1`, sessionID).Scan(&bound); err != nil || bound != device {
		t.Fatal("Session bound to", bound, err)
	}
}
