package deploymentpg_test

import (
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"sync"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

func runningPressureOwner(t *testing.T, f fixture, changes *deployment.ExecutionOperations, installation, node string, generation uint64) deployment.Allocation {
	t.Helper()
	key := hostedEnvironment(t, f.pool)
	if _, err := f.pool.Exec(t.Context(), `INSERT INTO runtime_placements(environment_id,node_id,deployment_generation) VALUES($1,$2,$3)`, key.EnvironmentID, node, generation); err != nil {
		t.Fatal(err)
	}
	owner, err := changes.ReserveAllocation(t.Context(), key, installation, credentialHash())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.pool.Exec(t.Context(), `UPDATE environments SET initialization='complete' WHERE id=$1`, key.EnvironmentID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.pool.Exec(t.Context(), `UPDATE runtime_allocations SET state='running',create_settled=true,compute_phase='running',compute_activity_at=clock_timestamp()-interval '1 minute',compute_phase_changed_at=clock_timestamp()-interval '1 minute' WHERE id=$1`, owner.ID); err != nil {
		t.Fatal(err)
	}
	owner, err = f.adapter.EnvironmentAllocation(t.Context(), key)
	if err != nil {
		t.Fatal(err)
	}
	return owner
}

func TestPressureSuspensionRequiresUsefulCapacity(t *testing.T) {
	for _, test := range []struct {
		name             string
		retained         int
		demand, freeNode bool
		want             error
	}{
		{"no demand", 3, false, false, deployment.ErrNotIdle},
		{"retained full", 1, true, false, deployment.ErrNotIdle},
		{"other node free", 3, true, true, deployment.ErrNotIdle},
		{"waiting first session", 3, true, false, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newFixture(t)
			changes, _ := f.execution(t)
			installation, view := f.initialize(t, changes, sandbox.Selection{Provider: "microsandbox", DeploymentSpec: testSpecification("microsandbox")})
			node := f.enroll(t, view, deployment.Capacity{MaxActive: 1, MaxRetained: test.retained})
			f.connect(t, node.NodeID)
			owner := runningPressureOwner(t, f, changes, installation, node.NodeID, view.Generation)
			if test.demand {
				pendingPressureDemand(t, f)
			}
			if test.freeNode {
				other := f.enroll(t, view, deployment.Capacity{MaxActive: 1, MaxRetained: 3})
				f.connect(t, other.NodeID)
			}
			until := time.Now().Add(time.Hour)
			_, err := changes.SetCompute(t.Context(), owner, "quiescing", json.RawMessage(`{}`), &until, 5*time.Minute)
			if !errors.Is(err, test.want) {
				t.Fatalf("quiesce: %v, want %v", err, test.want)
			}
			nodes, err := f.adapter.Nodes(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			for _, n := range nodes {
				if n.ID == node.NodeID && (n.Active != 1 || n.Retained != 1) {
					t.Fatalf("unconfirmed suspension freed capacity: %#v", n)
				}
			}
		})
	}
}

func TestPressureSuspensionSerializesAcrossNodes(t *testing.T) {
	f := newFixture(t)
	changes, _ := f.execution(t)
	installation, view := f.initialize(t, changes, sandbox.Selection{Provider: "microsandbox", DeploymentSpec: testSpecification("microsandbox")})
	owners := make([]deployment.Allocation, 2)
	for i := range owners {
		node := f.enroll(t, view, deployment.Capacity{MaxActive: 1, MaxRetained: 3})
		f.connect(t, node.NodeID)
		owners[i] = runningPressureOwner(t, f, changes, installation, node.NodeID, view.Generation)
	}
	pendingPressureDemand(t, f)
	var wg sync.WaitGroup
	errs := make([]error, len(owners))
	for i, owner := range owners {
		wg.Go(func() {
			until := time.Now().Add(time.Hour)
			_, errs[i] = changes.SetCompute(t.Context(), owner, "quiescing", json.RawMessage(`{}`), &until, 5*time.Minute)
		})
	}
	wg.Wait()
	success := 0
	for _, err := range errs {
		if err == nil {
			success++
		} else if !errors.Is(err, deployment.ErrNotIdle) {
			t.Fatal(err)
		}
	}
	if success != 1 {
		t.Fatalf("one waiting Session started %d suspensions: %v", success, errs)
	}
	var phases int
	if err := f.pool.QueryRow(t.Context(), `SELECT count(*) FROM runtime_allocations WHERE compute_phase='quiescing'`).Scan(&phases); err != nil || phases != 1 {
		t.Fatal(phases, err)
	}
}

func pendingPressureDemand(t *testing.T, f fixture) deployment.AllocationKey {
	t.Helper()
	key := hostedEnvironment(t, f.pool)
	if _, err := f.pool.Exec(t.Context(), `UPDATE environments SET initialization='pending' WHERE id=$1`, key.EnvironmentID); err != nil {
		t.Fatal(err)
	}
	return key
}

func TestPressureSuspensionServesRestoreOnItsOriginalNode(t *testing.T) {
	f := newFixture(t)
	changes, _ := f.execution(t)
	installation, view := f.initialize(t, changes, sandbox.Selection{Provider: "microsandbox", DeploymentSpec: testSpecification("microsandbox")})
	node := f.enroll(t, view, deployment.Capacity{MaxActive: 1, MaxRetained: 2})
	f.connect(t, node.NodeID)
	waiting := runningPressureOwner(t, f, changes, installation, node.NodeID, view.Generation)
	if _, err := f.pool.Exec(t.Context(), `UPDATE runtime_allocations SET compute_phase='suspended',compute_retained_until=clock_timestamp()+interval '1 hour',compute_wake_requested=true WHERE id=$1`, waiting.ID); err != nil {
		t.Fatal(err)
	}
	owner := runningPressureOwner(t, f, changes, installation, node.NodeID, view.Generation)
	// Free capacity on another node cannot restore this retained compute.
	other := f.enroll(t, view, deployment.Capacity{MaxActive: 1, MaxRetained: 2})
	f.connect(t, other.NodeID)
	until := time.Now().Add(time.Hour)
	if _, err := changes.SetCompute(t.Context(), owner, "quiescing", json.RawMessage(`{}`), &until, 5*time.Minute); err != nil {
		t.Fatal(err)
	}
	var wake bool
	if err := f.pool.QueryRow(t.Context(), `SELECT compute_wake_requested FROM runtime_allocations WHERE id=$1`, waiting.ID).Scan(&wake); err != nil || !wake {
		t.Fatal("pressure consumed the waiting wake", wake, err)
	}
}

func TestPressureDemandScansPastIncompatiblePage(t *testing.T) {
	for _, retained := range []bool{false, true} {
		t.Run(map[bool]string{false: "active", true: "retained"}[retained], func(t *testing.T) {
			f := newFixture(t)
			changes, _ := f.execution(t)
			installation, view := f.initialize(t, changes, sandbox.Selection{Provider: "microsandbox", DeploymentSpec: retainedSpecification()})
			capacity := deployment.Capacity{MaxActive: 1, MaxRetained: 2}
			if retained {
				capacity.MaxRetained = 1
			}
			node := f.enroll(t, view, capacity)
			f.connect(t, node.NodeID)
			owner := runningPressureOwner(t, f, changes, installation, node.NodeID, view.Generation)
			if retained {
				owner = qualifyPressureRetention(t, f, owner)
			}
			for range 32 {
				key := pendingPressureDemand(t, f)
				if _, err := f.pool.Exec(t.Context(), `UPDATE sessions SET engine='mcode' WHERE id=(SELECT session_id FROM environments WHERE id=$1)`, key.EnvironmentID); err != nil {
					t.Fatal(err)
				}
			}
			pendingPressureDemand(t, f)
			until := time.Now().Add(time.Hour)
			var result deployment.Allocation
			var err error
			if retained {
				result, err = changes.EndRetentionForDemand(t.Context(), owner)
			} else {
				result, err = changes.SetCompute(t.Context(), owner, "quiescing", json.RawMessage(`{}`), &until, 5*time.Minute)
			}
			if err != nil {
				t.Fatal(err)
			}
			if retained && (result.ComputeRetainedUntil == nil || !result.ComputeRetainedUntil.Before(*owner.ComputeRetainedUntil)) {
				t.Fatal("later compatible demand did not end retention")
			}
			if !retained && result.ComputePhase != "quiescing" {
				t.Fatal("later compatible demand did not suspend compute")
			}
		})
	}
}

func TestPressureDemandRejectsUnqualifiedRetainedReceipt(t *testing.T) {
	for _, badReceipt := range []string{"history", "credential"} {
		t.Run(badReceipt, func(t *testing.T) {
			f := newFixture(t)
			changes, _ := f.execution(t)
			installation, view := f.initialize(t, changes, sandbox.Selection{Provider: "microsandbox", DeploymentSpec: retainedSpecification()})
			node := f.enroll(t, view, deployment.Capacity{MaxActive: 1, MaxRetained: 2})
			f.connect(t, node.NodeID)
			count := 1
			if badReceipt == "history" {
				count = 32
			}
			for range count {
				old := runningPressureOwner(t, f, changes, installation, node.NodeID, view.Generation)
				old = releaseRetainedFixture(t, f, changes, old)
				if _, err := f.pool.Exec(t.Context(), `UPDATE runtime_allocations SET compute_wake_requested=true WHERE id=$1`, old.ID); err != nil {
					t.Fatal(err)
				}
				statement := `UPDATE devices SET supported_agent_kinds='[]' WHERE id=$1`
				if badReceipt == "credential" {
					statement = `UPDATE devices SET revoked_at=NULL WHERE id=$1`
				}
				if _, err := f.pool.Exec(t.Context(), statement, old.DeviceID); err != nil {
					t.Fatal(err)
				}
			}
			owner := runningPressureOwner(t, f, changes, installation, node.NodeID, view.Generation)
			until := time.Now().Add(time.Hour)
			if _, err := changes.SetCompute(t.Context(), owner, "quiescing", json.RawMessage(`{}`), &until, 5*time.Minute); !errors.Is(err, deployment.ErrNotIdle) {
				t.Fatal("unqualified receipt suspended healthy owner", err)
			}
			// An empty qualified page must not stop the cursor before a later
			// valid request, even though the raw first page was nonempty.
			pendingPressureDemand(t, f)
			if _, err := changes.SetCompute(t.Context(), owner, "quiescing", json.RawMessage(`{}`), &until, 5*time.Minute); err != nil {
				t.Fatal("later valid demand was hidden", err)
			}
		})
	}
}

func TestPressureReclamationIgnoresUnavailableNodeReceipts(t *testing.T) {
	for _, obstruction := range []string{"offline expired", "offline quiescing", "offline suspending", "different address", "incompatible generation"} {
		for _, retention := range []bool{false, true} {
			t.Run(obstruction+map[bool]string{false: "/suspend", true: "/retention"}[retention], func(t *testing.T) {
				f := newFixture(t)
				changes, _ := f.execution(t)
				spec := retainedSpecification()
				if obstruction == "incompatible generation" {
					spec = testSpecification("microsandbox")
				}
				installation, view := f.initialize(t, changes, sandbox.Selection{Provider: "microsandbox", DeploymentSpec: spec})
				badNode := f.enroll(t, view, deployment.Capacity{MaxActive: 1, MaxRetained: 2})
				f.connect(t, badNode.NodeID)
				stuck := runningPressureOwner(t, f, changes, installation, badNode.NodeID, view.Generation)
				phase := "quiescing"
				if obstruction == "offline suspending" {
					phase = "suspending"
				}
				if _, err := f.pool.Exec(t.Context(), `UPDATE runtime_allocations SET compute_phase=$2,compute_retained_until=clock_timestamp()+interval '1 hour' WHERE id=$1`, stuck.ID, phase); err != nil {
					t.Fatal(err)
				}
				switch obstruction {
				case "offline expired", "offline quiescing", "offline suspending":
					if _, err := f.pool.Exec(t.Context(), `UPDATE runtime_nodes SET connection_id=NULL WHERE id=$1`, badNode.NodeID); err != nil {
						t.Fatal(err)
					}
					if obstruction == "offline expired" {
						if _, err := f.pool.Exec(t.Context(), `UPDATE runtime_allocations SET compute_retained_until=clock_timestamp()-interval '1 minute' WHERE id=$1`, stuck.ID); err != nil {
							t.Fatal(err)
						}
					}
				case "different address":
					if _, err := f.pool.Exec(t.Context(), `UPDATE runtime_nodes SET core_url='https://other.example' WHERE id=$1`, badNode.NodeID); err != nil {
						t.Fatal(err)
					}
				case "incompatible generation":
					var err error
					view, err = changes.Update(admin(t), installation, sandbox.Selection{Provider: "microsandbox", ExpectedGeneration: view.Generation, DeploymentSpec: retainedSpecification()})
					if err != nil {
						t.Fatal(err)
					}
				}
				limit := 2
				if retention {
					limit = 1
				}
				node := f.enroll(t, view, deployment.Capacity{MaxActive: 1, MaxRetained: limit})
				f.connect(t, node.NodeID)
				var waiting deployment.AllocationKey
				if obstruction == "incompatible generation" {
					old := releaseRetainedFixture(t, f, changes, runningPressureOwner(t, f, changes, installation, node.NodeID, view.Generation))
					waiting = old.Key()
				} else {
					waiting = pendingPressureDemand(t, f)
				}
				// A real pending reservation supplies demand and retains its original deadline.
				if _, err := f.pool.Exec(t.Context(), `INSERT INTO environment_input_reservations(id,session_id,idempotency_key,batch,created_at,deadline) SELECT $1,session_id,'pressure','[{}]',clock_timestamp(),clock_timestamp()+interval '5 minutes' FROM environments WHERE id=$2`, uuid.NewString(), waiting.EnvironmentID); err != nil {
					t.Fatal(err)
				}
				owner := runningPressureOwner(t, f, changes, installation, node.NodeID, view.Generation)
				if retention {
					owner = qualifyPressureRetention(t, f, owner)
				}
				until := time.Now().Add(time.Hour)
				if retention {
					if _, err := changes.EndRetentionForDemand(t.Context(), owner); err != nil {
						t.Fatal(err)
					}
					current, err := f.adapter.EnvironmentAllocation(t.Context(), owner.Key())
					if err != nil || !current.Expired {
						t.Fatal("healthy retention did not advance", current, err)
					}
					pending, err := changes.RequestCleanup(t.Context(), current)
					if err != nil {
						t.Fatal(err)
					}
					// Supply the native deletion receipt for the healthy fixture only.
					if _, err = changes.ReleaseAllocation(t.Context(), pending); err != nil {
						t.Fatal(err)
					}
				} else {
					current, err := changes.SetCompute(t.Context(), owner, "quiescing", json.RawMessage(`{}`), &until, 5*time.Minute)
					if err != nil {
						t.Fatal("healthy suspension blocked", err)
					}
					// Supply healthy native quiesce/capture/stop receipts through the
					// existing fenced phase transitions; never settle the stuck owner.
					for _, next := range []string{"suspending", "suspended"} {
						current, err = changes.SetCompute(t.Context(), current, next, json.RawMessage(`{}`), &until, 5*time.Minute)
						if err != nil {
							t.Fatal(err)
						}
					}
				}
				reserved, err := changes.EnsurePlacement(t.Context(), waiting, installation)
				if err != nil || reserved.NodeID != node.NodeID {
					t.Fatal("waiting input did not advance on healthy node", reserved, err)
				}
				observed, err := f.adapter.EnvironmentAllocation(t.Context(), stuck.Key())
				if err != nil || observed.ID != stuck.ID || observed.DeviceID != stuck.DeviceID || observed.State != "running" || observed.ComputePhase != phase {
					t.Fatal("changed unknown receipt", observed, err)
				}
				nodes, err := f.adapter.Nodes(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				for _, n := range nodes {
					if n.ID == badNode.NodeID && (n.Active != 1 || n.Retained != 1) {
						t.Fatal("released unknown capacity", n)
					}
				}
			})
		}
	}
}
