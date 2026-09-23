package store_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

func TestManagedNodesIsolateBlockedProviderAndInitialization(t *testing.T) {
	for _, mode := range []string{"observe", "initialize"} {
		t.Run(mode, func(t *testing.T) {
			f := newNodeIsolationFixture(t, mode)
			// Prepare a complete snapshot through the original phase/receipt path.
			wakeTenant, _, wakeEnv := f.session(f.nodeB, false)
			wakeOwner := f.provision(wakeTenant, wakeEnv)
			f.phase(wakeTenant, wakeEnv.ID, "running")
			if _, err := f.pool.Exec(t.Context(), "INSERT INTO turns(id,session_id,status,completed_at) VALUES($1,$2,'completed',clock_timestamp()-interval '2 minutes')", uuid.NewString(), wakeOwner.SessionID); err != nil {
				t.Fatal(err)
			}
			if _, err := f.pool.Exec(t.Context(), "UPDATE runtime_allocations SET compute_activity_at=clock_timestamp()-interval '2 minutes' WHERE id=$1", wakeOwner.ID); err != nil {
				t.Fatal(err)
			}
			f.phase(wakeTenant, wakeEnv.ID, "suspended")
			deleteTenant, deleteSession, deleteEnv := f.session(f.nodeB, false)
			f.provision(deleteTenant, deleteEnv)
			f.phase(deleteTenant, deleteEnv.ID, "running")

			count := 33 // More than one full page on A must never consume B's cursor.
			if mode == "initialize" {
				count = 1
			}
			for range count {
				tenant, _, env := f.session(f.nodeA, mode == "initialize")
				f.provision(tenant, env)
				f.provider.blocked[env.ID] = true
			}
			f.provider.armed.Store(true)
			f.run()
			select {
			case <-f.provider.entered:
			case <-time.After(8 * time.Second):
				t.Fatal("node A did not enter blocked provider")
			}
			online, err := f.store.RuntimeNodeAvailable(t.Context(), f.nodeA)
			if err != nil || !online {
				t.Fatal("test node must stay online while its helper is blocked", err)
			}

			// Same-node direct callers and the scan all consume the same one-shot
			// allocation receipt. A's in-flight provider operation remains blocked.
			tenant, _, env := f.session(f.nodeB, true)
			f.provider.mu.Lock()
			before := f.provider.creates
			f.provider.mu.Unlock()
			var callers sync.WaitGroup
			results := make(chan store.RuntimeAllocation, 12)
			failures := make(chan error, 12)
			for range 12 {
				callers.Add(1)
				go func() {
					defer callers.Done()
					ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
					defer cancel()
					owner, err := f.worker.ProvisionEnvironment(ctx, tenant, env.ID, f.key)
					results <- owner
					failures <- err
				}()
			}
			callers.Wait()
			close(results)
			close(failures)
			for err := range failures {
				if err != nil {
					t.Fatal("healthy node direct provisioning blocked", err)
				}
			}
			allocation := ""
			for owner := range results {
				if allocation == "" {
					allocation = owner.ID
				}
				if owner.ID != allocation || owner.NodeID != f.nodeB {
					t.Fatal("direct callers replaced allocation or placement")
				}
			}
			f.provider.mu.Lock()
			created := f.provider.creates - before
			f.provider.mu.Unlock()
			if created != 1 {
				t.Fatalf("concurrent direct/scan replayed Create: %d", created)
			}
			if err := f.store.TouchRuntimeActivity(t.Context(), wakeTenant, wakeEnv.ID); err != nil {
				t.Fatal(err)
			}
			if err := f.store.DeleteSession(t.Context(), deleteTenant, deleteSession.ID); err != nil {
				t.Fatal(err)
			}

			// Only independent normal five-second loops drive these transitions.
			// No manual reconciliation or wake hint accelerates healthy nodes.
			waitNodeIsolation(t, 18*time.Second, func() (bool, string) {
				wake, e1 := f.store.GetRuntimeAllocation(t.Context(), wakeTenant, wakeEnv.ID)
				deleted, e2 := f.store.GetRuntimeAllocation(t.Context(), deleteTenant, deleteEnv.ID)
				initialized, e3 := f.store.GetRuntimeAllocation(t.Context(), tenant, env.ID)
				f.provider.mu.Lock()
				restores := f.provider.restores
				f.provider.mu.Unlock()
				state := fmt.Sprintf("wake=%s/%s err=%v; deleted=%s/%s err=%v; initialized=%s/%s/%s err=%v; restores=%d writes=%d blocked_returns=%d", wake.State, wake.ComputePhase, e1, deleted.State, deleted.ComputePhase, e2, initialized.State, initialized.Initialization, initialized.ComputePhase, e3, restores, f.provider.writes.Load(), f.provider.returned.Load())
				return e1 == nil && e2 == nil && e3 == nil && wake.ComputePhase == "running" && deleted.State == "released" && initialized.Initialization == "complete" && initialized.ComputePhase == "running", state
			})
			if f.provider.returned.Load() != 0 || f.provider.writes.Load() != 1 {
				t.Fatalf("A returned early or initialization replayed: returned=%d writes=%d", f.provider.returned.Load(), f.provider.writes.Load())
			}
			f.provider.mu.Lock()
			restores := f.provider.restores
			f.provider.mu.Unlock()
			if restores != 1 || f.provider.promptFrames.Load() != 0 {
				t.Fatal("restore replayed or lifecycle sent model work")
			}

			// A newly enrolled node receives its own lane while A is still stuck.
			nodeC := uuid.NewString()
			f.enroll(nodeC)
			f.online(nodeC)
			ct, cs, ce := f.session(nodeC, false)
			f.provision(ct, ce)
			if err := f.store.DeleteSession(t.Context(), ct, cs.ID); err != nil {
				t.Fatal(err)
			}
			waitNodeIsolation(t, 7*time.Second, func() (bool, string) {
				owner, err := f.store.GetRuntimeAllocation(t.Context(), ct, ce.ID)
				return err == nil && owner.State == "released", fmt.Sprintf("new node allocation=%s/%s err=%v", owner.State, owner.ComputePhase, err)
			})
			if err := f.store.RemoveRuntimeNode(t.Context(), nodeC); err != nil {
				t.Fatal(err)
			}
			f.stop()
		})
	}
}

func TestManagedNodesOwnerLossStopsAllLanes(t *testing.T) {
	f := newNodeIsolationFixture(t, "observe")
	tenant, _, env := f.session(f.nodeA, false)
	f.provision(tenant, env)
	f.provider.blocked[env.ID] = true
	f.provider.armed.Store(true)
	f.run()
	select {
	case <-f.provider.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("blocked provider not entered")
	}
	// Terminate only this fixture database's execution-owner advisory lock.
	_, err := f.pool.Exec(t.Context(), "SELECT pg_terminate_backend(pid) FROM pg_locks WHERE locktype='advisory' AND database=(SELECT oid FROM pg_database WHERE datname=current_database()) AND classid=(706172736172::bigint >> 32)::oid AND objid=(706172736172::bigint & 4294967295)::oid")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-f.runDone:
		if err == nil || errors.Is(err, context.Canceled) {
			t.Fatalf("owner loss did not fail worker: %v", err)
		}
		f.stopOnce.Do(func() {})
	case <-time.After(5 * time.Second):
		t.Fatal("owner loss did not cancel/drain blocked node")
	}
	if f.provider.returned.Load() != 1 {
		t.Fatal("owner loss left provider call running")
	}
}
