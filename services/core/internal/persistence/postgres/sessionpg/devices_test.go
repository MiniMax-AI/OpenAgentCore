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

// newAgentHost stores an agent host of tenant, or of none when tenant is not
// valid, and returns its ID.
func newAgentHost(t *testing.T, pool *pgxpool.Pool, tenant pgtype.UUID) string {
	t.Helper()
	id := uuid.NewString()
	exec(t, pool, `INSERT INTO devices(id, tenant_id, name, credential_hash, agent_host) VALUES ($1, $2, 'agent host', $3, true)`, id, tenant, strings.Repeat("a", 64))
	return id
}

// enroll gives the self_hosted Environment a live enrollment Link resource.
func enroll(t *testing.T, pool *pgxpool.Pool, tenant, session, environment pgtype.UUID) {
	t.Helper()
	key := uuid.New()
	exec(t, pool, `UPDATE sessions SET creator_kind = 'user', creator_id = 'owner' WHERE id = $1`, session)
	exec(t, pool, `INSERT INTO execution_project_scopes(tenant_id, organization_id, project_id) VALUES ($1, 'org', $2)`, tenant, uuid.UUID(tenant.Bytes).String())
	exec(t, pool, `INSERT INTO environment_executor_credentials(key_id, tenant_id, subject_kind, subject_id, environment_id, token_sha256, created_at, issued_at)
		VALUES ($1, $2, 'user', 'owner', $3, $4, clock_timestamp(), clock_timestamp())`, key, tenant, environment, strings.ReplaceAll(uuid.NewString()+uuid.NewString(), "-", ""))
	exec(t, pool, `INSERT INTO sandbox_enrollments(id, environment_id, executor_key_id) VALUES ($1, $2, $3)`, uuid.New(), environment, key)
}

// Placement binds a Session to an agent host only, and a Session with an
// Environment only while that Environment has a live Link resource.
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
	tenantID, sessionID, environmentID := newEnvironment(t, pool, "self_hosted", "pending")
	tenant, session := uuidText(tenantID), uuidText(sessionID)
	otherTenant, _, _ := newEnvironment(t, pool, "self_hosted", "pending")
	device, replacement := newAgentHost(t, pool, pgtype.UUID{}), newAgentHost(t, pool, tenantID)
	revoked := newAgentHost(t, pool, pgtype.UUID{})
	exec(t, pool, `UPDATE devices SET revoked_at = clock_timestamp() WHERE id = $1`, revoked)
	tenantDevice := uuid.NewString()
	exec(t, pool, `INSERT INTO devices(id, tenant_id, name, credential_hash) VALUES ($1, $2, 'runtime', $3)`, tenantDevice, tenantID, strings.Repeat("a", 64))

	for name, test := range map[string]struct {
		device string
		want   error
	}{
		"another tenant's agent host":         {newAgentHost(t, pool, otherTenant), sessions.ErrNotFound},
		"revoked agent host":                  {revoked, sessions.ErrNotFound},
		"device that is no agent host":        {tenantDevice, sessions.ErrNotFound},
		"malformed device":                    {"device", sessions.ErrInvalidInput},
		"Environment without a live resource": {device, sessions.ErrDeviceBindingConflict},
	} {
		if err := operations.BindSessionDevice(t.Context(), tenant, session, test.device); !errors.Is(err, test.want) {
			t.Fatalf("%s: %v, want %v", name, err, test.want)
		}
	}
	enroll(t, pool, tenantID, sessionID, environmentID)
	for range 2 {
		if err := operations.BindSessionDevice(t.Context(), tenant, session, device); err != nil {
			t.Fatal("binding the bound agent host again", err)
		}
	}
	if err := operations.BindSessionDevice(t.Context(), tenant, session, replacement); !errors.Is(err, sessions.ErrDeviceBindingConflict) {
		t.Fatal("rebinding the Session to another agent host", err)
	}
	var bound string
	if err := pool.QueryRow(t.Context(), `SELECT runtime_id::text FROM session_runtime_assignments WHERE session_id = $1`, sessionID).Scan(&bound); err != nil || bound != device {
		t.Fatal("Session bound to", bound, err)
	}
}
