package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment/placement"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

func deploymentSelection() deployment.ProcessDeployment {
	return deployment.ProcessDeployment{InstallationID: uuid.NewString(), BackendFingerprint: strings.Repeat("a", 64)}
}

func deploymentConfigure(t *testing.T, w *Store, config *deployment.ProcessDeployment) {
	t.Helper()
	if err := deploymentExecution(t, w).ConfigureProcess(t.Context(), config); err != nil {
		t.Fatal(err)
	}
	if config != nil && config.ProviderKind != "" {
		legacyRuntimeSpecification(t, w, config.ProviderKind)
	}
}

// Lower-level legacy fixtures supply verified metadata without replaying the
// configuration transition being tested. Web setup owns this write in production.
func legacyRuntimeSpecification(t *testing.T, w *Store, provider string) {
	t.Helper()
	spec := SandboxDeploymentTestSpec(provider)
	raw, _ := json.Marshal(spec)
	if _, err := w.pool.Exec(t.Context(), "UPDATE runtime_deployment SET specification=$1", raw); err != nil {
		t.Fatal(err)
	}
	if _, err := w.pool.Exec(t.Context(), "UPDATE runtime_nodes SET deployment_generation=1,ready_generation=1,specification_digest=$1", spec.Digest(provider)); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeDeploymentPendingSessionsCannotMigrate(t *testing.T) {
	s, _ := newManagedTestStore(t)
	w := executionWriter(t, s)
	tenant := uuid.NewString()
	session, _ := localEnvironment(t, s, tenant)
	old := deploymentSelection()
	// First selection is allowed for work that has never had an installation.
	deploymentConfigure(t, w, &old)
	old.AdmissionPaused = true
	deploymentConfigure(t, w, &old)
	next := deploymentSelection()
	next.AdmissionPaused = true
	if err := deploymentExecution(t, w).ConfigureProcess(t.Context(), &next); err == nil || !strings.Contains(err.Error(), "1 pending hosted") {
		t.Fatal("pending Session migrated", err)
	}
	if err := deploymentExecution(t, w).ConfigureProcess(t.Context(), nil); err == nil {
		t.Fatal("pending Session orphaned by removing provider")
	}
	if err := s.DeleteSession(t.Context(), tenant, session.ID); err != nil {
		t.Fatal(err)
	}
	deploymentConfigure(t, w, &next)
}

func TestRuntimeDeploymentUnknownAllocationsBlockAdoptionAndSwitch(t *testing.T) {
	s, _ := newManagedTestStore(t)
	w := executionWriter(t, s)
	old := deploymentSelection()
	tenant := uuid.NewString()
	session, environment := localEnvironment(t, s, tenant)
	owner, err := deploymentExecution(t, w).ReserveAllocation(t.Context(), deployment.AllocationKey{TenantID: tenant, EnvironmentID: environment.ID}, old.InstallationID, runtimedevice.HashCredential(uuid.NewString()))
	if err != nil {
		t.Fatal(err)
	}
	if err := deploymentExecution(t, w).ConfigureProcess(t.Context(), &old); err == nil || !strings.Contains(err.Error(), "no verified backend identity") {
		t.Fatal("legacy allocation silently adopted", err)
	}
	if err := s.DeleteSession(t.Context(), tenant, session.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := deploymentExecution(t, w).RequestCleanup(t.Context(), owner); err != nil {
		t.Fatal(err)
	}
	if _, err := deploymentExecution(t, w).ReleaseAllocation(t.Context(), owner); !errors.Is(err, deployment.ErrAllocationConflict) {
		t.Fatal("unknown creation lost cleanup ownership", err)
	}
	if err := deploymentExecution(t, w).ConfigureProcess(t.Context(), &old); err == nil {
		t.Fatal("deleted unknown allocation did not block adoption")
	}
	if _, err := deploymentExecution(t, w).SettleCreation(t.Context(), owner); err != nil {
		t.Fatal(err)
	}
	if _, err := deploymentExecution(t, w).ReleaseAllocation(t.Context(), owner); err != nil {
		t.Fatal(err)
	}
	deploymentConfigure(t, w, &old)
	_, environment = localEnvironment(t, s, tenant)
	owner, err = deploymentExecution(t, w).ReserveAllocation(t.Context(), deployment.AllocationKey{TenantID: tenant, EnvironmentID: environment.ID}, old.InstallationID, runtimedevice.HashCredential(uuid.NewString()))
	if err != nil {
		t.Fatal(err)
	}
	old.AdmissionPaused = true
	deploymentConfigure(t, w, &old)
	next := deploymentSelection()
	next.AdmissionPaused = true
	if err := deploymentExecution(t, w).ConfigureProcess(t.Context(), &next); err == nil || !strings.Contains(err.Error(), "1 unreleased allocations") {
		t.Fatal("unknown creation did not block switch", err)
	}
	if err := deploymentExecution(t, w).ConfigureProcess(t.Context(), nil); err == nil {
		t.Fatal("removing adapter orphaned unknown creation")
	}
	replay, err := deploymentExecution(t, w).ReserveAllocation(t.Context(), deployment.AllocationKey{TenantID: tenant, EnvironmentID: environment.ID}, old.InstallationID, runtimedevice.HashCredential(uuid.NewString()))
	if err != nil || !replay.Replayed || replay.ID != owner.ID {
		t.Fatal("maintenance blocked receipt replay", replay, err)
	}
	if _, err := deploymentExecution(t, w).RequestCleanup(t.Context(), owner); err != nil {
		t.Fatal("maintenance blocked cleanup", err)
	}
}

func TestRuntimeDeploymentMaintenancePreservesCreationRetriesAndOtherPlacements(t *testing.T) {
	s, pool := newManagedTestStore(t)
	w := executionWriter(t, s)
	old := deploymentSelection()
	deploymentConfigure(t, w, &old)
	tenant := uuid.NewString()
	input := sessions.CreateSession{Creator: FixtureCreator(), Engine: "codex", IdempotencyKey: uuid.NewString(), Configuration: json.RawMessage(`{"agent":{"model":"test"},"environment":{"type":"openai_hosted"}}`)}
	existing, err := s.CreateSession(t.Context(), tenant, input)
	if err != nil {
		t.Fatal(err)
	}
	old.AdmissionPaused = true
	deploymentConfigure(t, w, &old)
	replay, err := s.CreateSession(t.Context(), tenant, input)
	if err != nil || replay.ID != existing.ID {
		t.Fatal("creation retry lost identity", err)
	}
	input.IdempotencyKey = uuid.NewString()
	if _, err := s.CreateSession(t.Context(), tenant, input); !errors.Is(err, placement.ErrAdmissionClosed) {
		t.Fatal("maintenance created hosted Session", err)
	}
	var count int
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM sessions WHERE tenant_id=$1", tenant).Scan(&count); err != nil || count != 1 {
		t.Fatal("rejection left partial Session", count, err)
	}
	if _, err := deploymentExecution(t, w).ReserveAllocation(t.Context(), deployment.AllocationKey{TenantID: tenant, EnvironmentID: existing.Environment.ID}, old.InstallationID, runtimedevice.HashCredential(uuid.NewString())); !errors.Is(err, placement.ErrAdmissionClosed) {
		t.Fatal("maintenance reserved new allocation", err)
	}
	for _, kind := range []string{"none", "self_hosted"} {
		input.IdempotencyKey = uuid.NewString()
		input.Configuration = json.RawMessage(`{"agent":{"model":"test"},"environment":{"type":"` + kind + `"}}`)
		if _, err := s.CreateSession(t.Context(), tenant, input); err != nil {
			t.Fatal("maintenance blocked unrelated placement", kind, err)
		}
	}
	old.AdmissionPaused = false
	deploymentConfigure(t, w, &old)
	if _, err := deploymentExecution(t, w).ReserveAllocation(t.Context(), deployment.AllocationKey{TenantID: tenant, EnvironmentID: existing.Environment.ID}, uuid.NewString(), runtimedevice.HashCredential(uuid.NewString())); !errors.Is(err, placement.ErrAdmissionClosed) {
		t.Fatal("wrong installation reserved resource", err)
	}
	if _, err := deploymentExecution(t, w).ReserveAllocation(t.Context(), deployment.AllocationKey{TenantID: tenant, EnvironmentID: existing.Environment.ID}, old.InstallationID, runtimedevice.HashCredential(uuid.NewString())); err != nil {
		t.Fatal("resume did not reopen allocation", err)
	}
}

func TestRuntimeDeploymentMaintenanceSerializesHostedCreation(t *testing.T) {
	s, pool := newManagedTestStore(t)
	w := executionWriter(t, s)
	config := deploymentSelection()
	deploymentConfigure(t, w, &config)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	var blocker int32
	if err := tx.QueryRow(ctx, "SELECT pg_backend_pid() FROM runtime_deployment FOR UPDATE").Scan(&blocker); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	tenant := uuid.NewString()
	go func() {
		_, err := s.CreateSession(ctx, tenant, sessions.CreateSession{Creator: FixtureCreator(), Engine: "codex", IdempotencyKey: uuid.NewString(), Configuration: json.RawMessage(`{"environment":{"type":"openai_hosted"}}`)})
		done <- err
	}()
	runtimeSuspensionWaitBlocked(t, ctx, pool, blocker, done)
	if _, err := tx.Exec(ctx, "UPDATE runtime_deployment SET admission_paused=true"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, placement.ErrAdmissionClosed) {
		t.Fatal("creation bypassed committed maintenance", err)
	}
	var count int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM sessions WHERE tenant_id=$1", tenant).Scan(&count); err != nil || count != 0 {
		t.Fatal("racing creation left partial work", count, err)
	}
}

func TestRuntimeDeploymentRetainedResourcesBlockSwitchWithoutMutation(t *testing.T) {
	for _, state := range []string{"running", "suspended", "cleanup_pending"} {
		t.Run(state, func(t *testing.T) {
			s, pool := newManagedTestStore(t)
			w := executionWriter(t, s)
			old := deploymentSelection()
			deploymentConfigure(t, w, &old)
			tenant := uuid.NewString()
			_, environment := localEnvironment(t, s, tenant)
			owner, err := deploymentExecution(t, w).ReserveAllocation(t.Context(), deployment.AllocationKey{TenantID: tenant, EnvironmentID: environment.ID}, old.InstallationID, runtimedevice.HashCredential(uuid.NewString()))
			if err != nil {
				t.Fatal(err)
			}
			if state == "suspended" {
				_, err = pool.Exec(t.Context(), `UPDATE runtime_allocations SET state='running',create_settled=true,compute_phase='suspended',compute_state='{"snapshot":{"id":"retained-test-snapshot"}}' WHERE id=$1`, owner.ID)
			} else {
				_, err = pool.Exec(t.Context(), "UPDATE runtime_allocations SET state=$2,create_settled=true WHERE id=$1", owner.ID, state)
			}
			if err != nil {
				t.Fatal(err)
			}
			old.AdmissionPaused = true
			deploymentConfigure(t, w, &old)
			var before, after, oldIdentity, newIdentity string
			if err := pool.QueryRow(t.Context(), "SELECT to_jsonb(a)::text FROM runtime_allocations a WHERE id=$1", owner.ID).Scan(&before); err != nil {
				t.Fatal(err)
			}
			if err := pool.QueryRow(t.Context(), "SELECT to_jsonb(d)::text FROM runtime_deployment d").Scan(&oldIdentity); err != nil {
				t.Fatal(err)
			}
			next := deploymentSelection()
			next.AdmissionPaused = true
			if err := deploymentExecution(t, w).ConfigureProcess(t.Context(), &next); err == nil || !strings.Contains(err.Error(), "1 unreleased allocations") {
				t.Fatal("retained resource allowed switch", state, err)
			}
			if err := pool.QueryRow(t.Context(), "SELECT to_jsonb(a)::text FROM runtime_allocations a WHERE id=$1", owner.ID).Scan(&after); err != nil {
				t.Fatal(err)
			}
			if err := pool.QueryRow(t.Context(), "SELECT to_jsonb(d)::text FROM runtime_deployment d").Scan(&newIdentity); err != nil {
				t.Fatal(err)
			}
			if before != after || oldIdentity != newIdentity {
				t.Fatal("refused switch changed existing ownership")
			}
		})
	}
}

func TestRuntimeDeploymentAllocationBeforeMaintenanceRetainsOwnership(t *testing.T) {
	s, pool := newManagedTestStore(t)
	w := executionWriter(t, s)
	config := deploymentSelection()
	deploymentConfigure(t, w, &config)
	tenant := uuid.NewString()
	_, environment := localEnvironment(t, s, tenant)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	var blocker int32
	if err := tx.QueryRow(ctx, "SELECT pg_backend_pid() FROM runtime_deployment FOR UPDATE").Scan(&blocker); err != nil {
		t.Fatal(err)
	}
	allocated := make(chan error, 1)
	go func() {
		_, err := deploymentExecution(t, w).ReserveAllocation(ctx, deployment.AllocationKey{TenantID: tenant, EnvironmentID: environment.ID}, config.InstallationID, runtimedevice.HashCredential(uuid.NewString()))
		allocated <- err
	}()
	runtimeSuspensionWaitBlocked(t, ctx, pool, blocker, allocated)
	maintaining := make(chan error, 1)
	maintenance := config
	maintenance.AdmissionPaused = true
	changes := deploymentExecution(t, w)
	go func() { maintaining <- changes.ConfigureProcess(ctx, &maintenance) }()
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-allocated; err != nil {
		t.Fatal("earlier allocation lost ownership", err)
	}
	if err := <-maintaining; err != nil {
		t.Fatal(err)
	}
	owner, err := deploymentStore(w).EnvironmentAllocation(ctx, deployment.AllocationKey{TenantID: tenant, EnvironmentID: environment.ID})
	if err != nil || owner.State != "creating" || owner.CreateSettled {
		t.Fatal("maintenance changed uncertain receipt", owner, err)
	}
	next := deploymentSelection()
	next.AdmissionPaused = true
	if err := deploymentExecution(t, w).ConfigureProcess(ctx, &next); err == nil || !strings.Contains(err.Error(), "1 unreleased allocations") {
		t.Fatal("earlier in-flight allocation omitted from switch guard", err)
	}
}
