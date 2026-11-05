package deploymentpg_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
)

// standaloneEnvironment stores an initialized standalone Environment of a
// fresh tenant, with no Session, and returns its key.
func standaloneEnvironment(t *testing.T, f fixture) deployment.AllocationKey {
	t.Helper()
	tenant, environment := uuid.New(), uuid.New()
	if _, err := f.pool.Exec(t.Context(), `INSERT INTO environments(id, tenant_id, configuration, session_owned, initialization, idempotency_key, request_hash, creator_kind, creator_id)
		VALUES ($1, $2, '{"type":"openai_hosted"}', false, 'complete', 'key', repeat('a', 64), 'user', 'creator')`, environment, tenant); err != nil {
		t.Fatal(err)
	}
	return deployment.AllocationKey{TenantID: tenant.String(), EnvironmentID: environment.String()}
}

// reserveStandalone reserves compute for a standalone Environment and
// settles it as running idle compute in phase.
func reserveStandalone(t *testing.T, f fixture, changes *deployment.ExecutionOperations, installation, phase string) deployment.Allocation {
	t.Helper()
	key := standaloneEnvironment(t, f)
	owner, err := changes.ReserveAllocation(t.Context(), key, installation, credentialHash())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), `UPDATE runtime_allocations SET state='running', create_settled=true, compute_phase=$2,
		compute_retained_until = CASE WHEN $2 = 'running' OR $2 = 'disabled' THEN NULL ELSE clock_timestamp() - interval '1 second' END,
		compute_activity_at=clock_timestamp() - interval '1 day', compute_phase_changed_at=clock_timestamp() - interval '1 day' WHERE id=$1`, owner.ID, phase); err != nil {
		t.Fatal(err)
	}
	owner, err = f.adapter.EnvironmentAllocation(t.Context(), key)
	if err != nil {
		t.Fatal(err)
	}
	return owner
}

func environmentStatus(t *testing.T, f fixture, key deployment.AllocationKey) string {
	t.Helper()
	var status string
	if err := f.pool.QueryRow(t.Context(), "SELECT status FROM environments WHERE id=$1", key.EnvironmentID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	return status
}

// An unattached Environment provisions without a Session, and without
// checkpoints expires once it stays idle for the idle timeout.
func TestUnattachedEnvironmentExpiresAtTheIdleTimeout(t *testing.T) {
	f := newFixture(t)
	changes, _ := f.execution(t)
	installation, _ := f.initialize(t, changes, setupE2BSelection())
	owner := reserveStandalone(t, f, changes, installation, "disabled")
	if owner.AttachedSessionID != "" || owner.Reclaimed {
		t.Fatal("standalone allocation reads attached or reclaimed", owner)
	}
	if _, err := f.pool.Exec(t.Context(), `UPDATE runtime_allocations SET compute_activity_at=clock_timestamp(), compute_phase_changed_at=clock_timestamp(), created_at=clock_timestamp() WHERE id=$1`, owner.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := changes.ExpireIdleUnattached(t.Context(), owner); !errors.Is(err, deployment.ErrNotIdle) {
		t.Fatal("expired before the idle timeout", err)
	}
	if _, err := f.pool.Exec(t.Context(), `UPDATE runtime_allocations SET compute_activity_at=clock_timestamp() - interval '1 day', compute_phase_changed_at=clock_timestamp() - interval '1 day', created_at=clock_timestamp() - interval '1 day' WHERE id=$1`, owner.ID); err != nil {
		t.Fatal(err)
	}
	expired, err := changes.ExpireIdleUnattached(t.Context(), owner)
	if err != nil || expired.State != "cleanup_pending" || environmentStatus(t, f, owner.Key()) != "expired" {
		t.Fatal("idle Environment did not expire", expired, err)
	}
}

// Confirmed compute loss expires an unattached Environment; it is never
// replaced on new compute.
func TestUnattachedEnvironmentExpiresOnConfirmedComputeLoss(t *testing.T) {
	f := newFixture(t)
	changes, _ := f.execution(t)
	installation, _ := f.initialize(t, changes, setupE2BSelection())
	owner := reserveStandalone(t, f, changes, installation, "running")
	released, err := changes.ReleaseLostCompute(t.Context(), owner)
	if err != nil || released.State != "released" || environmentStatus(t, f, owner.Key()) != "expired" {
		t.Fatal("lost compute did not expire the Environment", released, err)
	}
}

// With checkpoints, the end of retention expires an unattached Environment.
func TestUnattachedEnvironmentExpiresWhenRetentionEnds(t *testing.T) {
	f := newFixture(t)
	changes, _ := f.execution(t)
	installation, _ := f.initialize(t, changes, setupE2BSelection())
	owner := reserveStandalone(t, f, changes, installation, "suspended")
	if !owner.Expired {
		t.Fatal("retention did not end", owner)
	}
	cleaned, err := changes.RequestCleanup(t.Context(), owner)
	if err != nil || cleaned.State != "cleanup_pending" || environmentStatus(t, f, owner.Key()) != "expired" {
		t.Fatal("retention end did not expire the Environment", cleaned, err)
	}
}

// Deleting the attached Session starts the idle clock of an Environment that
// outlives it, so it does not expire the moment it detaches.
func TestDetachStartsTheIdleClock(t *testing.T) {
	f := newFixture(t)
	changes, _ := f.execution(t)
	installation, _ := f.initialize(t, changes, setupE2BSelection())
	owner := reserveStandalone(t, f, changes, installation, "disabled")
	session := uuid.New()
	if _, err := f.pool.Exec(t.Context(), `INSERT INTO sessions(id, tenant_id, engine, idempotency_key, request_hash, configuration, environment_id)
		VALUES ($1, $2, 'codex', 'key', 'hash', '{}', $3)`, session, owner.Key().TenantID, owner.Key().EnvironmentID); err != nil {
		t.Fatal(err)
	}
	if err := sqlc.New(f.pool).RecordStandaloneDetach(t.Context(), pgtype.UUID{Bytes: session, Valid: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), `UPDATE sessions SET deleted_at=now() WHERE id=$1`, session); err != nil {
		t.Fatal(err)
	}
	if _, err := changes.ExpireIdleUnattached(t.Context(), owner); !errors.Is(err, deployment.ErrNotIdle) {
		t.Fatal("expired right after the detach", err)
	}
}

// A failed unattached Environment releases its compute without waiting for the
// idle timeout.
func TestFailedUnattachedEnvironmentReleasesItsCompute(t *testing.T) {
	f := newFixture(t)
	changes, _ := f.execution(t)
	installation, _ := f.initialize(t, changes, setupE2BSelection())
	owner := reserveStandalone(t, f, changes, installation, "disabled")
	if _, err := f.pool.Exec(t.Context(), `UPDATE runtime_allocations SET compute_activity_at=clock_timestamp(), compute_phase_changed_at=clock_timestamp(), created_at=clock_timestamp() WHERE id=$1`, owner.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), `UPDATE environments SET status='failed' WHERE id=$1`, owner.Key().EnvironmentID); err != nil {
		t.Fatal(err)
	}
	released, err := changes.ExpireIdleUnattached(t.Context(), owner)
	if err != nil || released.State != "cleanup_pending" || environmentStatus(t, f, owner.Key()) != "failed" {
		t.Fatal("failed Environment kept its compute", released, err)
	}
}
