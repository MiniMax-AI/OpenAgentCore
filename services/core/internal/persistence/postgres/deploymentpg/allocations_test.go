package deploymentpg_test

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/sessionpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

// hostedEnvironment stores a fresh tenant's hosted Session with its pending
// Environment and returns the Environment's key.
func hostedEnvironment(t *testing.T, pool *pgxpool.Pool) deployment.AllocationKey {
	t.Helper()
	tenant, session, environment := uuid.New(), uuid.New(), uuid.New()
	if _, err := pool.Exec(t.Context(), `INSERT INTO sessions(id, tenant_id, engine, idempotency_key, request_hash, configuration)
		VALUES ($1, $2, 'codex', 'key', 'hash', '{"environment":{"type":"openai_hosted"}}')`, session, tenant); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `INSERT INTO environments(id, session_id) VALUES ($1, $2)`, environment, session); err != nil {
		t.Fatal(err)
	}
	return deployment.AllocationKey{TenantID: tenant.String(), EnvironmentID: environment.String()}
}

// credentialHash is the digest of a fresh Serve credential.
func credentialHash() string {
	digest := sha256.Sum256([]byte(uuid.NewString()))
	return hex.EncodeToString(digest[:])
}

// allocationRows reports the allocation, assignment release, Environment
// status and Session journal length.
func allocationRows(t *testing.T, f fixture, key deployment.AllocationKey) (allocations int, state string, released bool, status string, changes int) {
	t.Helper()
	err := f.pool.QueryRow(t.Context(), `SELECT
		(SELECT count(*) FROM runtime_allocations WHERE environment_id = e.id),
		COALESCE((SELECT state FROM runtime_allocations WHERE environment_id = e.id), ''),
		COALESCE((SELECT desired_state = 'released' FROM session_runtime_assignments WHERE session_id = e.session_id), false),
		e.status,
		(SELECT count(*) FROM session_events WHERE session_id = e.session_id)
		FROM environments e WHERE e.id = $1`, key.EnvironmentID).Scan(&allocations, &state, &released, &status, &changes)
	if err != nil {
		t.Fatal(err)
	}
	return
}

// Concurrent reservations of one Environment serialize on its Session: one
// commits the allocation, the others replay it, and another
// installation conflicts.
func TestConcurrentReservationsCommitOneAllocation(t *testing.T) {
	f := newFixture(t)
	changes, _ := f.execution(t)
	installation, _ := f.initialize(t, changes, setupE2BSelection())
	key := hostedEnvironment(t, f.pool)
	results := make([]deployment.Allocation, 8)
	errs := make([]error, len(results))
	var wg sync.WaitGroup
	for i := range results {
		wg.Go(func() {
			results[i], errs[i] = changes.ReserveAllocation(t.Context(), key, installation, credentialHash())
		})
	}
	wg.Wait()
	fresh := 0
	for i, result := range results {
		if errs[i] != nil || result.ID != results[0].ID {
			t.Fatal("reservations disagree", result, errs[i])
		}
		if !result.Replayed {
			fresh++
		}
	}
	var devices int
	if err := f.pool.QueryRow(t.Context(), "SELECT count(*) FROM devices").Scan(&devices); err != nil {
		t.Fatal(err)
	}
	if allocations, state, _, _, _ := allocationRows(t, f, key); fresh != 1 || allocations != 1 || devices != 0 || state != "creating" {
		t.Fatal("reservations committed more than one allocation", fresh, allocations, devices, state)
	}
	if _, err := changes.ReserveAllocation(t.Context(), key, uuid.NewString(), credentialHash()); !errors.Is(err, deployment.ErrAllocationConflict) {
		t.Fatal("another installation replayed the allocation", err)
	}
}

// Every allocation write needs the execution lease; after the lease is lost
// none of them commits.
func TestAllocationWritesNeedTheLease(t *testing.T) {
	f := newFixture(t)
	closed := setupClosedExecution(t, f)
	changes, _ := f.execution(t)
	installation, _ := f.initialize(t, changes, setupE2BSelection())
	key := hostedEnvironment(t, f.pool)
	owner, err := changes.ReserveAllocation(t.Context(), key, installation, credentialHash())
	if err != nil {
		t.Fatal(err)
	}
	unallocated := hostedEnvironment(t, f.pool)
	if _, err := closed.ReserveAllocation(t.Context(), unallocated, installation, credentialHash()); !errors.Is(err, pgunit.ErrLeaseClosed) {
		t.Fatal("reserved without the lease", err)
	}
	if allocations, _, _, _, _ := allocationRows(t, f, unallocated); allocations != 0 {
		t.Fatal("a reservation without the lease committed")
	}
	writes := map[string]func() error{
		"ObserveRunning": func() error { _, err := closed.ObserveRunning(t.Context(), owner); return err },
		"SettleCreation": func() error { _, err := closed.SettleCreation(t.Context(), owner); return err },
		"RequestCleanup": func() error { _, err := closed.RequestCleanup(t.Context(), owner); return err },
		"SetCompute": func() error {
			_, err := closed.SetCompute(t.Context(), owner, "running", []byte(`{}`), nil, 0)
			return err
		},
		"ClearWake": func() error { return closed.ClearWake(t.Context(), owner, time.Now()) },
	}
	for name, write := range writes {
		if err := write(); !errors.Is(err, pgunit.ErrLeaseClosed) {
			t.Fatal(name, "wrote without the lease", err)
		}
	}
	if current, err := f.adapter.EnvironmentAllocation(t.Context(), key); err != nil || current.State != "creating" || current.CreateSettled || current.ComputePhase != owner.ComputePhase {
		t.Fatal("a write without the lease committed", current, err)
	}
}

// Cleanup releases the assignment, settles the Session and requests cleanup
// in one transaction: a failed settlement rolls the release back.
func TestFailedCleanupSettlementRollsBackAssignmentRelease(t *testing.T) {
	f := newFixture(t)
	changes, lease := f.execution(t)
	installation, _ := f.initialize(t, changes, setupE2BSelection())
	key := hostedEnvironment(t, f.pool)
	owner, err := changes.ReserveAllocation(t.Context(), key, installation, credentialHash())
	if err != nil {
		t.Fatal(err)
	}
	if owner, err = changes.ObserveRunning(t.Context(), owner); err != nil {
		t.Fatal(err)
	}
	host := uuid.NewString()
	if _, err := f.pool.Exec(t.Context(), `INSERT INTO devices(id, name, credential_hash) VALUES ($1, 'host', $2)`, host, credentialHash()); err != nil {
		t.Fatal(err)
	}
	execution, err := sessions.NewExecutionOperations(sessionpg.NewExecution(lease))
	if err != nil {
		t.Fatal(err)
	}
	if err := execution.BindSessionDevice(t.Context(), owner.TenantID, owner.SessionID, host); err != nil {
		t.Fatal(err)
	}
	_, _, _, _, before := allocationRows(t, f, key)
	if _, err := f.pool.Exec(t.Context(), `CREATE FUNCTION fail_environment_update() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected settlement failure'; END $$;
		CREATE TRIGGER fail_environment_update BEFORE UPDATE ON environments FOR EACH ROW EXECUTE FUNCTION fail_environment_update()`); err != nil {
		t.Fatal(err)
	}
	if _, err := changes.RequestCleanup(t.Context(), owner); err == nil {
		t.Fatal("cleanup committed without the Session settlement")
	}
	if _, state, released, status, changed := allocationRows(t, f, key); state != "running" || released || status != "pending" || changed != before {
		t.Fatal("failed cleanup left a partial commit", state, released, status, changed)
	}
	if _, err := f.pool.Exec(t.Context(), "DROP TRIGGER fail_environment_update ON environments"); err != nil {
		t.Fatal(err)
	}
	pending, err := changes.RequestCleanup(t.Context(), owner)
	if err != nil || pending.State != "cleanup_pending" {
		t.Fatal(pending, err)
	}
	if _, state, released, status, changed := allocationRows(t, f, key); state != "cleanup_pending" || !released || status != "failed" || changed <= before {
		t.Fatal("cleanup did not commit together", state, released, status, changed)
	}
	var removeHome, revoked bool
	var epoch int64
	if err := f.pool.QueryRow(t.Context(), `SELECT a.remove_home, a.epoch, d.revoked_at IS NOT NULL FROM session_runtime_assignments a JOIN devices d ON d.id = a.runtime_id WHERE a.session_id = $1`, owner.SessionID).Scan(&removeHome, &epoch, &revoked); err != nil || removeHome || revoked || epoch != 2 {
		t.Fatal("cleanup changed host authority or home removal", removeHome, revoked, epoch, err)
	}
}

// An allocation write commits only with the Session journal prune: a failed
// prune rolls back a change and a reservation.
func TestFailedPruneRollsBackTheAllocationWrite(t *testing.T) {
	f := newFixture(t)
	changes, _ := f.execution(t)
	installation, _ := f.initialize(t, changes, setupE2BSelection())
	key := hostedEnvironment(t, f.pool)
	owner, err := changes.ReserveAllocation(t.Context(), key, installation, credentialHash())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), `CREATE FUNCTION fail_prune() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected prune failure'; END $$;
		CREATE TRIGGER fail_prune BEFORE DELETE ON session_events FOR EACH STATEMENT EXECUTE FUNCTION fail_prune()`); err != nil {
		t.Fatal(err)
	}
	if _, err := changes.ObserveRunning(t.Context(), owner); err == nil || !strings.Contains(err.Error(), "injected prune failure") {
		t.Fatal("a change committed without the prune", err)
	}
	if _, state, _, _, _ := allocationRows(t, f, key); state != "creating" {
		t.Fatal("a failed prune kept the change", state)
	}
	unallocated := hostedEnvironment(t, f.pool)
	if _, err := changes.ReserveAllocation(t.Context(), unallocated, installation, credentialHash()); err == nil || !strings.Contains(err.Error(), "injected prune failure") {
		t.Fatal("a reservation committed without the prune", err)
	}
	var devices int
	if err := f.pool.QueryRow(t.Context(), "SELECT count(*) FROM devices").Scan(&devices); err != nil {
		t.Fatal(err)
	}
	if allocations, _, _, _, _ := allocationRows(t, f, unallocated); allocations != 0 || devices != 0 {
		t.Fatal("a failed prune kept the reservation", allocations, devices)
	}
}
