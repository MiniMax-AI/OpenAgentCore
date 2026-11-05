package deploymentpg_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/sessionpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
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
	if _, err := changes.ExpireIdleUnattached(t.Context(), owner, true); !errors.Is(err, deployment.ErrNotIdle) {
		t.Fatal("expired before the idle timeout", err)
	}
	if _, err := f.pool.Exec(t.Context(), `UPDATE runtime_allocations SET compute_activity_at=clock_timestamp() - interval '1 day', compute_phase_changed_at=clock_timestamp() - interval '1 day', created_at=clock_timestamp() - interval '1 day' WHERE id=$1`, owner.ID); err != nil {
		t.Fatal(err)
	}
	expired, err := changes.ExpireIdleUnattached(t.Context(), owner, true)
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

// attachSession stores a live Session attached to the Environment.
func attachSession(t *testing.T, f fixture, key deployment.AllocationKey) uuid.UUID {
	t.Helper()
	session := uuid.New()
	if _, err := f.pool.Exec(t.Context(), `INSERT INTO sessions(id, tenant_id, engine, idempotency_key, request_hash, configuration, environment_id)
		VALUES ($1, $2, 'codex', 'key', 'hash', '{}', $3)`, session, key.TenantID, key.EnvironmentID); err != nil {
		t.Fatal(err)
	}
	return session
}

// deleteSession deletes the Session through the storage path of the public delete.
func deleteSession(t *testing.T, f fixture, key deployment.AllocationKey, session uuid.UUID) {
	t.Helper()
	store := sessionpg.New(pgunit.NewPool(f.pool), f.cipher)
	if err := store.WithSessionDeletion(t.Context(), key.TenantID, session.String(), func(ctx context.Context, _ sessions.LockedSession, tx sessions.SessionDeletionTx) error {
		return tx.ApplyDeletion(ctx)
	}); err != nil {
		t.Fatal(err)
	}
}

// Deleting the attached Session starts the idle clock of an Environment that
// outlives it, so it does not expire the moment it detaches.
func TestDetachStartsTheIdleClock(t *testing.T) {
	f := newFixture(t)
	changes, _ := f.execution(t)
	installation, _ := f.initialize(t, changes, setupE2BSelection())
	owner := reserveStandalone(t, f, changes, installation, "disabled")
	// The compute has been idle for a day, so only the detach can make it busy.
	if _, err := f.pool.Exec(t.Context(), `UPDATE runtime_allocations SET created_at=clock_timestamp() - interval '1 day' WHERE id=$1`, owner.ID); err != nil {
		t.Fatal(err)
	}
	deleteSession(t, f, owner.Key(), attachSession(t, f, owner.Key()))
	if _, err := changes.ExpireIdleUnattached(t.Context(), owner, true); !errors.Is(err, deployment.ErrNotIdle) {
		t.Fatal("expired right after the detach", err)
	}
}

// An Environment whose compute was released while a Session held it expires
// when the Session detaches, so its workspace is deleted and a reset does not
// wait for it.
func TestDetachAfterReleasedComputeExpiresTheEnvironment(t *testing.T) {
	f := newFixture(t)
	changes, _ := f.execution(t)
	installation, view := f.initialize(t, changes, setupE2BSelection())
	owner := reserveStandalone(t, f, changes, installation, "suspended")
	config := uuid.New()
	for _, statement := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO workspace_fs_configurations(id,adapter,parameters) VALUES($1,'fixture','{}')`, []any{config}},
		{`INSERT INTO environment_workspaces(object_id,environment_id,configuration_id,state,attachment) VALUES($1,$2,$3,'ready','{}')`, []any{uuid.New(), owner.EnvironmentID, config}},
		{`UPDATE runtime_allocations SET state='released', released_at=clock_timestamp() WHERE id=$1`, []any{owner.ID}},
	} {
		if _, err := f.pool.Exec(t.Context(), statement.sql, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	session := attachSession(t, f, owner.Key())
	if status := environmentStatus(t, f, owner.Key()); status != "pending" {
		t.Fatal("the held Environment ended", status)
	}
	deleteSession(t, f, owner.Key(), session)
	if status := environmentStatus(t, f, owner.Key()); status != "expired" {
		t.Fatal("the detached Environment stayed", status)
	}
	candidates, err := sqlc.New(f.pool).ListWorkspaceDeletionCandidates(t.Context(), pgtype.UUID{Valid: true})
	if err != nil || len(candidates) != 1 {
		t.Fatal("workspace is not a deletion candidate", candidates, err)
	}
	if err := changes.StartReset(admin(t), installation, deployment.ResetRequest{ExpectedGeneration: view.Generation, Clear: deployment.ResetForce}); err != nil {
		t.Fatal(err)
	}
	reset, err := f.service.View(t.Context())
	if err != nil || reset.Reset == nil || reset.Reset.Remaining.Idle+reset.Reset.Remaining.Busy+reset.Reset.Remaining.Cleanup != 0 {
		t.Fatal("the reset still waits", reset.Reset, err)
	}
	if _, err := changes.CompleteReset(t.Context(), installation, view.Generation, reset.Reset.RequestedAt); err != nil {
		t.Fatal(err)
	}
}

// A failed unattached Environment releases its compute without waiting for the
// idle timeout, also where the Provider suspends idle compute.
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
	released, err := changes.ExpireIdleUnattached(t.Context(), owner, false)
	if err != nil || released.State != "cleanup_pending" || environmentStatus(t, f, owner.Key()) != "failed" {
		t.Fatal("failed Environment kept its compute", released, err)
	}
}

// Without checkpoints, the idle clock of an unattached Environment starts when
// its initialization completes, not when its compute was created.
func TestInitializationStartsTheIdleClock(t *testing.T) {
	f := newFixture(t)
	changes, lease := f.execution(t)
	installation, _ := f.initialize(t, changes, setupE2BSelection())
	owner := reserveStandalone(t, f, changes, installation, "disabled")
	if _, err := f.pool.Exec(t.Context(), `UPDATE runtime_allocations SET created_at=clock_timestamp() - interval '1 day' WHERE id=$1`, owner.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), `UPDATE environments SET initialization='running' WHERE id=$1`, owner.Key().EnvironmentID); err != nil {
		t.Fatal(err)
	}
	key := owner.Key()
	if err := sessionpg.NewExecution(lease).WithInitialization(t.Context(), key.TenantID, key.EnvironmentID, func(ctx context.Context, tx sessions.InitializationTx, _ sessions.LockedSession) error {
		_, err := tx.CompleteInitialization(ctx, key.EnvironmentID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := changes.ExpireIdleUnattached(t.Context(), owner, true); !errors.Is(err, deployment.ErrNotIdle) {
		t.Fatal("expired as soon as it was ready", err)
	}
}

// Detaching an Environment that ends for lack of compute also releases the
// placement it held for new compute.
func TestDetachReleasesTheHeldPlacementOfAnEndedEnvironment(t *testing.T) {
	f := newFixture(t)
	changes, _ := f.execution(t)
	installation, view := f.initialize(t, changes, sandbox.Selection{Provider: "microsandbox", DeploymentSpec: testSpecification("microsandbox")})
	node := f.enroll(t, view, deployment.Capacity{MaxActive: 1, MaxRetained: 1})
	f.connect(t, node.NodeID)
	key := standaloneEnvironment(t, f)
	if _, err := f.pool.Exec(t.Context(), `INSERT INTO runtime_placements(environment_id,node_id,deployment_generation) VALUES($1,$2,$3)`, key.EnvironmentID, node.NodeID, view.Generation); err != nil {
		t.Fatal(err)
	}
	owner, err := changes.ReserveAllocation(t.Context(), key, installation, credentialHash())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), `UPDATE runtime_allocations SET state='released', create_settled=true, released_at=clock_timestamp() WHERE id=$1`, owner.ID); err != nil {
		t.Fatal(err)
	}
	deleteSession(t, f, key, attachSession(t, f, key))
	var held int
	if err := f.pool.QueryRow(t.Context(), `SELECT count(*) FROM runtime_placements WHERE environment_id=$1 AND released_at IS NULL`, key.EnvironmentID).Scan(&held); err != nil || held != 0 || environmentStatus(t, f, key) != "expired" {
		t.Fatal("the ended Environment kept its placement", held, err)
	}
}
