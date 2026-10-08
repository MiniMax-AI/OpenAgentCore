package integration

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgtest"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/google/uuid"
)

func TestRuntimeAllocationAtomicOwnershipAndRecovery(t *testing.T) {
	s, provider := configuredStore(t)
	pool := s.pool
	w := executionWriter(t, s)
	tenant := uuid.NewString()
	session, environment := localEnvironment(t, s, tenant)
	secret := uuid.NewString()
	owner, err := deploymentExecution(t, w).ReserveAllocation(t.Context(), deployment.AllocationKey{TenantID: tenant, EnvironmentID: environment.ID}, provider, runtimedevice.HashCredential(secret))
	if err != nil || owner.Replayed || owner.State != "creating" || owner.CreateSettled {
		t.Fatalf("reservation: %+v %v", owner, err)
	}
	if _, err := sessionAdapter(s).GetSessionDevice(t.Context(), tenant, session.ID); !errors.Is(err, sessions.ErrNotFound) {
		t.Fatalf("the reservation bound the Session: %v", err)
	}
	if _, err := deploymentExecution(t, w).ReserveAllocation(t.Context(), deployment.AllocationKey{TenantID: uuid.NewString(), EnvironmentID: environment.ID}, provider, runtimedevice.HashCredential(secret)); !errors.Is(err, sessions.ErrNotFound) {
		t.Fatalf("foreign allocation accepted: %v", err)
	}
	if _, err := deploymentExecution(t, w).ReserveAllocation(t.Context(), deployment.AllocationKey{TenantID: tenant, EnvironmentID: environment.ID}, uuid.NewString(), runtimedevice.HashCredential(secret)); !errors.Is(err, deployment.ErrAllocationConflict) {
		t.Fatalf("provider target changed: %v", err)
	}
	awaitRelease := pgtest.ObserveExecutionLeaseRelease(t, w.pool)
	if err := w.lease.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	awaitRelease()
	if _, err := deploymentExecution(t, w).ObserveRunning(t.Context(), owner); err == nil {
		t.Fatal("lost writer changed allocation")
	}
	next := executionWriter(t, New(t, pool))
	retry, err := deploymentExecution(t, next).ReserveAllocation(t.Context(), deployment.AllocationKey{TenantID: tenant, EnvironmentID: environment.ID}, provider, runtimedevice.HashCredential(uuid.NewString()))
	if err != nil || !retry.Replayed || retry.ID != owner.ID {
		t.Fatalf("restart replaced unknown allocation: %+v %v", retry, err)
	}
	var credential string
	if err := pool.QueryRow(t.Context(), "SELECT serve_credential_hash FROM runtime_allocations WHERE id=$1", owner.ID).Scan(&credential); err != nil || credential != runtimedevice.HashCredential(secret) {
		t.Fatal("retry rewrote Serve credential", err)
	}
	if err := sessionService(t, s).DeleteSession(t.Context(), sessions.DeleteSessionCommand{TenantID: tenant, SessionID: session.ID}); err != nil {
		t.Fatal(err)
	}
	retained, err := deploymentStore(next).EnvironmentAllocation(t.Context(), deployment.AllocationKey{TenantID: tenant, EnvironmentID: environment.ID})
	if err != nil || !retained.SessionDeleted || retained.ID != owner.ID {
		t.Fatalf("deletion discarded cleanup identity: %+v %v", retained, err)
	}
	if _, err := deploymentExecution(t, next).ObserveRunning(t.Context(), owner); !errors.Is(err, sessions.ErrNotFound) {
		t.Fatalf("late creation revived deleted Session: %v", err)
	}
	if _, err := deploymentExecution(t, next).RequestCleanup(t.Context(), owner); err != nil {
		t.Fatal(err)
	}
	if _, err := deploymentExecution(t, next).ReleaseAllocation(t.Context(), owner); !errors.Is(err, deployment.ErrAllocationConflict) {
		t.Fatalf("unknown Create forgotten: %v", err)
	}
	found, cursor := false, ""
	for !found {
		rows, err := deploymentStore(next).CredentialAllocations(t.Context(), cursor)
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) == 0 {
			break
		}
		for _, row := range rows {
			found = found || row.ID == owner.ID
			cursor = row.ID
		}
	}

	if !found {
		t.Fatal("unknown cleanup absent from recovery scan")
	}
	if _, err := deploymentExecution(t, next).SettleCreation(t.Context(), owner); err != nil {
		t.Fatal(err)
	}
	if _, err := deploymentExecution(t, next).ReleaseAllocation(t.Context(), owner); err != nil {
		t.Fatal(err)
	}
	var releases int
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM runtime_allocations WHERE id=$1 AND released_at IS NOT NULL", owner.ID).Scan(&releases); err != nil || releases != 1 {
		t.Fatalf("cleanup confirmation not durable: %d %v", releases, err)
	}
}

func TestRuntimeAllocationOneWinnerAndRollback(t *testing.T) {
	s, provider := configuredStore(t)
	pool := s.pool
	w := executionWriter(t, s)
	tenant := uuid.NewString()
	_, environment := localEnvironment(t, s, tenant)
	var wg sync.WaitGroup
	results := make(chan deployment.Allocation, 8)
	errs := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			value, err := deploymentExecution(t, w).ReserveAllocation(t.Context(), deployment.AllocationKey{TenantID: tenant, EnvironmentID: environment.ID}, provider, runtimedevice.HashCredential(uuid.NewString()))
			results <- value
			errs <- err
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	winners, id := 0, ""
	for result := range results {
		if !result.Replayed {
			winners++
		}
		if id != "" && id != result.ID {
			t.Fatal("multiple allocation identities")
		}
		id = result.ID
	}
	if winners != 1 {
		t.Fatalf("%d fresh Create receipts", winners)
	}
	_, fail := localEnvironment(t, s, tenant)
	// Reject the reservation insert to prove that failed admission creates no allocation.
	constraint := "fixture_" + uuid.NewString()[:8]
	_, err := pool.Exec(t.Context(), "ALTER TABLE runtime_allocations ADD CONSTRAINT "+constraint+" CHECK (environment_id <> '"+fail.ID+"'::uuid)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "ALTER TABLE runtime_allocations DROP CONSTRAINT "+constraint)
	})
	if _, err := deploymentExecution(t, w).ReserveAllocation(t.Context(), deployment.AllocationKey{TenantID: tenant, EnvironmentID: fail.ID}, provider, runtimedevice.HashCredential(uuid.NewString())); err == nil {
		t.Fatal("injected insert failure succeeded")
	}
	var count int
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM runtime_allocations WHERE environment_id=$1", fail.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed reservation left an allocation: %d %v", count, err)
	}
}

func TestRuntimeAllocationCleanupReleasesAssignmentAndKeepsIdentity(t *testing.T) {
	s, installation := configuredStore(t)
	w := executionWriter(t, s)
	tenant := uuid.NewString()
	_, environment := localEnvironment(t, s, tenant)
	owner, err := deploymentExecution(t, w).ReserveAllocation(t.Context(), deployment.AllocationKey{TenantID: tenant, EnvironmentID: environment.ID}, installation, runtimedevice.HashCredential(uuid.NewString()))
	if err != nil {
		t.Fatal(err)
	}
	owner, err = deploymentExecution(t, w).ObserveRunning(t.Context(), owner)
	if err != nil || !owner.CreateSettled {
		t.Fatalf("running observation: %+v %v", owner, err)
	}
	if _, err := deploymentExecution(t, w).CheckRunning(t.Context(), owner); err != nil {
		t.Fatal(err)
	}
	if _, err := deploymentExecution(t, w).RequestCleanup(t.Context(), owner); err != nil {
		t.Fatal(err)
	}
	if _, err := deploymentExecution(t, w).CheckRunning(t.Context(), owner); !errors.Is(err, deployment.ErrAllocationConflict) {
		t.Fatalf("cleanup kept the allocation running: %v", err)
	}
	if _, err := deploymentExecution(t, w).ReleaseAllocation(t.Context(), owner); err != nil {
		t.Fatal(err)
	}
	got, err := deploymentExecution(t, w).ReserveAllocation(t.Context(), deployment.AllocationKey{TenantID: tenant, EnvironmentID: environment.ID}, owner.ProviderKey, runtimedevice.HashCredential(uuid.NewString()))
	if err != nil || !got.Replayed || got.State != "released" {
		t.Fatalf("cleanup permitted replacement: %+v %v", got, err)
	}
}
