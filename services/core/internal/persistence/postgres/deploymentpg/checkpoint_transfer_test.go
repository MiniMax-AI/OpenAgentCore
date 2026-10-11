package deploymentpg_test

import (
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"sync"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment/placement"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

var checkpointClass = sandbox.CheckpointCompatibility{ArtifactDomain: "fixture-archive", ExecutionClass: "fixture-machine"}

func readyCheckpointNode(t *testing.T, f fixture, node deployment.Enrollment, view deployment.View, compatibility sandbox.CheckpointCompatibility) {
	t.Helper()
	connection := uuid.NewString()
	epoch, err := f.adapter.OwnerEpoch(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := f.service.ConnectNode(t.Context(), node.NodeID, connection, epoch); err != nil {
		t.Fatal(err)
	}
	if err := f.service.HeartbeatGenerations(t.Context(), node.NodeID, connection, epoch, deployment.NodeHealth{}, []sandbox.GenerationStatus{{Generation: view.Generation, SpecificationDigest: view.SpecificationDigest, State: "ready", Checkpoint: &compatibility}}); err != nil {
		t.Fatal(err)
	}
}

func suspendedCheckpointOwner(t *testing.T, f fixture, changes *deployment.ExecutionOperations, installation string, node deployment.Enrollment, view deployment.View) deployment.Allocation {
	t.Helper()
	owner := runningPressureOwner(t, f, changes, installation, node.NodeID, view.Generation)
	if _, err := f.pool.Exec(t.Context(), `UPDATE runtime_allocations SET compute_phase='suspended',compute_state='{"snapshot":{"ID":"snapshot","Compatibility":{"artifact_domain":"fixture-archive","execution_class":"fixture-machine"}}}',compute_retained_until=clock_timestamp()+interval '24 hours',compute_wake_requested=true WHERE id=$1`, owner.ID); err != nil {
		t.Fatal(err)
	}
	owner, err := f.adapter.EnvironmentAllocation(t.Context(), owner.Key())
	if err != nil {
		t.Fatal(err)
	}
	return owner
}

func TestCheckpointTransferPreservesAllocationAndFencesOldOwner(t *testing.T) {
	f := newFixture(t)
	changes, _ := f.execution(t)
	installation, view := f.initialize(t, changes, sandbox.Selection{Provider: "microsandbox", DeploymentSpec: retainedSpecification()})
	source := f.enroll(t, view, deployment.Capacity{MaxActive: 1, MaxRetained: 1})
	readyCheckpointNode(t, f, source, view, checkpointClass)
	target := f.enroll(t, view, deployment.Capacity{MaxActive: 1, MaxRetained: 1})
	readyCheckpointNode(t, f, target, view, checkpointClass)
	owner := suspendedCheckpointOwner(t, f, changes, installation, source, view)
	if _, err := f.pool.Exec(t.Context(), `UPDATE runtime_nodes SET connection_id=NULL WHERE id=$1`, source.NodeID); err != nil {
		t.Fatal(err)
	}
	intent := json.RawMessage(`{"snapshot":{"ID":"snapshot"},"restore_id":"attempt"}`)
	moved, err := changes.BeginRestore(t.Context(), owner, checkpointClass, intent)
	if err != nil {
		t.Fatal(err)
	}
	if moved.ID != owner.ID || moved.DeviceID != owner.DeviceID || moved.DeploymentGeneration != owner.DeploymentGeneration || moved.NodeID != target.NodeID || moved.ComputePhase != "restoring" || moved.ComputeRevision != owner.ComputeRevision+1 || !moved.ComputeRetainedUntil.Equal(*owner.ComputeRetainedUntil) {
		t.Fatal("lost restore ownership", moved)
	}
	var placementNode, credentialNode string
	if err := f.pool.QueryRow(t.Context(), `SELECT p.node_id::text,a.node_id::text FROM runtime_placements p JOIN runtime_allocations a ON a.environment_id=p.environment_id JOIN devices d ON d.id=a.device_id WHERE a.id=$1 AND d.revoked_at IS NULL`, owner.ID).Scan(&placementNode, &credentialNode); err != nil || placementNode != target.NodeID || credentialNode != target.NodeID {
		t.Fatal("placement/device routing split", placementNode, credentialNode, err)
	}
	nodes, err := f.adapter.Nodes(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range nodes {
		if n.ID == source.NodeID && (n.Active != 0 || n.Retained != 0) || n.ID == target.NodeID && (n.Active != 1 || n.Retained != 1) {
			t.Fatal("incorrect capacity", n)
		}
	}
	if _, err := changes.BeginRestore(t.Context(), owner, checkpointClass, intent); !errors.Is(err, deployment.ErrAllocationConflict) {
		t.Fatal("stale source moved again", err)
	}
	if _, err := changes.BeginRestore(t.Context(), moved, checkpointClass, intent); !errors.Is(err, deployment.ErrAllocationConflict) {
		t.Fatal("unknown restore moved again", err)
	}
	if _, err := changes.RelocateCleanup(t.Context(), moved, checkpointClass); !errors.Is(err, deployment.ErrAllocationConflict) {
		t.Fatal("unknown restore reassigned cleanup", err)
	}

}

func TestCheckpointTransferRequiresCompatibleGenerationAndCapacity(t *testing.T) {
	for _, obstruction := range []string{"archive", "execution", "generation", "connection", "offline", "retained full", "active full", "expired", "quiescing", "no demand", "reset", "deleted", "epoch"} {
		t.Run(obstruction, func(t *testing.T) {
			f := newFixture(t)
			changes, _ := f.execution(t)
			installation, view := f.initialize(t, changes, sandbox.Selection{Provider: "microsandbox", DeploymentSpec: retainedSpecification()})
			source := f.enroll(t, view, deployment.Capacity{MaxActive: 1, MaxRetained: 2})
			readyCheckpointNode(t, f, source, view, checkpointClass)
			target := f.enroll(t, view, deployment.Capacity{MaxActive: 1, MaxRetained: 1})
			readyCheckpointNode(t, f, target, view, checkpointClass)
			owner := suspendedCheckpointOwner(t, f, changes, installation, source, view)
			if _, err := f.pool.Exec(t.Context(), `UPDATE runtime_nodes SET connection_id=NULL WHERE id=$1`, source.NodeID); err != nil {
				t.Fatal(err)
			}
			var statement string
			arg := target.NodeID
			want := placement.ErrNodeUnavailable
			switch obstruction {
			case "archive":
				statement = `UPDATE runtime_node_generation_status SET checkpoint='{"artifact_domain":"other","execution_class":"fixture-machine"}' WHERE node_id=$1`
			case "execution":
				statement = `UPDATE runtime_node_generation_status SET checkpoint='{"artifact_domain":"fixture-archive","execution_class":"other"}' WHERE node_id=$1`
			case "generation":
				statement = `UPDATE runtime_node_generation_status SET generation=generation+1 WHERE node_id=$1`
			case "connection":
				statement = `UPDATE runtime_node_generation_status SET connection_id=gen_random_uuid() WHERE node_id=$1`
			case "offline":
				statement = `UPDATE runtime_nodes SET connection_id=NULL WHERE id=$1`
			case "retained full":
				suspendedCheckpointOwner(t, f, changes, installation, target, view)
			case "active full":
				runningPressureOwner(t, f, changes, installation, target.NodeID, view.Generation)
			case "expired":
				statement = `UPDATE runtime_allocations SET compute_retained_until=clock_timestamp()-interval '1 second' WHERE id=$1`
				arg = owner.ID
				want = deployment.ErrAllocationConflict
			case "quiescing":
				statement = `UPDATE runtime_allocations SET compute_phase='quiescing' WHERE id=$1`
				arg = owner.ID
				want = deployment.ErrAllocationConflict
			case "no demand":
				statement = `UPDATE runtime_allocations SET compute_wake_requested=false WHERE id=$1`
				arg = owner.ID
				want = deployment.ErrAllocationConflict
			case "deleted":
				statement = `UPDATE sessions SET deleted_at=clock_timestamp() WHERE id=(SELECT session_id FROM environments WHERE id=$1)`
				arg = owner.EnvironmentID
				want = sessions.ErrNotFound
			case "epoch":
				statement = `UPDATE runtime_node_generation_status SET owner_epoch=owner_epoch+1 WHERE node_id=$1`
			case "reset":
				statement = `UPDATE runtime_deployment SET reset_clear='force',reset_requested_at=clock_timestamp(),reset_forced_at=clock_timestamp(),reset_audit='{}' WHERE installation_id=$1`
				arg = installation
				want = placement.ErrResetAdmission
			}
			if statement != "" {
				if _, err := f.pool.Exec(t.Context(), statement, arg); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := changes.BeginRestore(t.Context(), owner, checkpointClass, json.RawMessage(`{"restore_id":"attempt"}`)); !errors.Is(err, want) {
				t.Fatal("invalid destination admitted", err, want)
			}
			current, err := f.adapter.EnvironmentAllocation(t.Context(), owner.Key())
			if err != nil || current.NodeID != source.NodeID || current.ComputeRevision != owner.ComputeRevision {
				t.Fatal("rejection changed ownership", current, err)
			}
		})
	}
}

func TestCheckpointTransfersSerializeDestinationCapacity(t *testing.T) {
	f := newFixture(t)
	changes, _ := f.execution(t)
	installation, view := f.initialize(t, changes, sandbox.Selection{Provider: "microsandbox", DeploymentSpec: retainedSpecification()})
	source := f.enroll(t, view, deployment.Capacity{MaxActive: 2, MaxRetained: 2})
	readyCheckpointNode(t, f, source, view, checkpointClass)
	target := f.enroll(t, view, deployment.Capacity{MaxActive: 1, MaxRetained: 1})
	readyCheckpointNode(t, f, target, view, checkpointClass)
	owners := []deployment.Allocation{suspendedCheckpointOwner(t, f, changes, installation, source, view), suspendedCheckpointOwner(t, f, changes, installation, source, view)}
	if _, err := f.pool.Exec(t.Context(), `UPDATE runtime_nodes SET connection_id=NULL WHERE id=$1`, source.NodeID); err != nil {
		t.Fatal(err)
	}
	errs := make([]error, len(owners))
	var wg sync.WaitGroup
	for i, owner := range owners {
		wg.Go(func() {
			_, errs[i] = changes.BeginRestore(t.Context(), owner, checkpointClass, json.RawMessage(`{"restore_id":"attempt"}`))
		})
	}
	wg.Wait()
	success := 0
	for _, err := range errs {
		if err == nil {
			success++
		} else if !errors.Is(err, placement.ErrNodeUnavailable) {
			t.Fatal(err)
		}
	}
	if success != 1 {
		t.Fatal("destination overbooked", errs)
	}
}

func TestCheckpointCleanupRelocatesArtifactWithoutExecutionCompatibility(t *testing.T) {
	f := newFixture(t)
	changes, _ := f.execution(t)
	installation, view := f.initialize(t, changes, sandbox.Selection{Provider: "microsandbox", DeploymentSpec: retainedSpecification()})
	source := f.enroll(t, view, deployment.Capacity{MaxActive: 1, MaxRetained: 1})
	readyCheckpointNode(t, f, source, view, checkpointClass)
	target := f.enroll(t, view, deployment.Capacity{MaxActive: 1, MaxRetained: 2})
	other := checkpointClass
	other.ExecutionClass = "other-machine"
	readyCheckpointNode(t, f, target, view, other)
	runningPressureOwner(t, f, changes, installation, target.NodeID, view.Generation)
	owner := suspendedCheckpointOwner(t, f, changes, installation, source, view)
	if _, err := f.pool.Exec(t.Context(), `UPDATE runtime_nodes SET connection_id=NULL WHERE id=$1`, source.NodeID); err != nil {
		t.Fatal(err)
	}
	if _, err := changes.RelocateCleanup(t.Context(), owner, checkpointClass); !errors.Is(err, deployment.ErrAllocationConflict) {
		t.Fatal("moved live allocation for cleanup", err)
	}
	cleanup, err := changes.RequestCleanup(t.Context(), owner)
	if err != nil {
		t.Fatal(err)
	}
	moved, err := changes.RelocateCleanup(t.Context(), cleanup, checkpointClass)
	if err != nil {
		t.Fatal(err)
	}
	if moved.NodeID != target.NodeID || moved.State != "cleanup_pending" || moved.ComputePhase != "suspended" || moved.ID != owner.ID || !moved.ComputeRetainedUntil.Equal(*owner.ComputeRetainedUntil) {
		t.Fatal("cleanup recreated compute", moved)
	}
	nodes, err := f.adapter.Nodes(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range nodes {
		if n.ID == target.NodeID && (n.Active != 1 || n.Retained != 2) {
			t.Fatal("cleanup released retained slot", n)
		}
	}
	if _, err := changes.ReleaseAllocation(t.Context(), moved); err != nil {
		t.Fatal(err)
	}
}

func TestCheckpointPressureSuspendsOnlyUsableDestination(t *testing.T) {
	for _, scenario := range []string{"waiting restore", "retained full", "incompatible", "other free"} {
		t.Run(scenario, func(t *testing.T) {
			f := newFixture(t)
			changes, _ := f.execution(t)
			installation, view := f.initialize(t, changes, sandbox.Selection{Provider: "microsandbox", DeploymentSpec: retainedSpecification()})
			source := f.enroll(t, view, deployment.Capacity{MaxActive: 1, MaxRetained: 1})
			readyCheckpointNode(t, f, source, view, checkpointClass)
			suspendedCheckpointOwner(t, f, changes, installation, source, view)
			if _, err := f.pool.Exec(t.Context(), `UPDATE runtime_nodes SET connection_id=NULL WHERE id=$1`, source.NodeID); err != nil {
				t.Fatal(err)
			}
			retained := 2
			if scenario == "retained full" {
				retained = 1
			}
			target := f.enroll(t, view, deployment.Capacity{MaxActive: 1, MaxRetained: retained})
			compatibility := checkpointClass
			if scenario == "incompatible" {
				compatibility.ExecutionClass = "other"
			}
			readyCheckpointNode(t, f, target, view, compatibility)
			active := runningPressureOwner(t, f, changes, installation, target.NodeID, view.Generation)
			if scenario == "other free" {
				other := f.enroll(t, view, deployment.Capacity{MaxActive: 1, MaxRetained: 1})
				readyCheckpointNode(t, f, other, view, checkpointClass)
			}
			until := time.Now().Add(24 * time.Hour)
			_, err := changes.SetCompute(t.Context(), active, "quiescing", json.RawMessage(`{}`), &until, 5*time.Minute)
			if scenario == "waiting restore" {
				if err != nil {
					t.Fatal("compatible wake cannot trigger pressure suspension", err)
				}
			} else if !errors.Is(err, deployment.ErrNotIdle) {
				t.Fatal("suspended without usable restore capacity", err)
			}
		})
	}
}

func TestCheckpointRestoreUsesRetainedSlotAndImmutableGeneration(t *testing.T) {
	f := newFixture(t)
	changes, _ := f.execution(t)
	installation, view := f.initialize(t, changes, sandbox.Selection{Provider: "microsandbox", DeploymentSpec: retainedSpecification()})
	source := f.enroll(t, view, deployment.Capacity{MaxActive: 1, MaxRetained: 1})
	readyCheckpointNode(t, f, source, view, checkpointClass)
	owner := suspendedCheckpointOwner(t, f, changes, installation, source, view)
	// A rollout must not substitute the serving generation for this snapshot.
	if _, err := f.pool.Exec(t.Context(), `UPDATE runtime_nodes SET ready_generation=ready_generation+1 WHERE id=$1`, source.NodeID); err != nil {
		t.Fatal(err)
	}
	restored, err := changes.BeginRestore(t.Context(), owner, checkpointClass, json.RawMessage(`{"restore_id":"attempt"}`))
	if err != nil {
		t.Fatal(err)
	}
	if restored.NodeID != source.NodeID || restored.DeploymentGeneration != owner.DeploymentGeneration {
		t.Fatal("changed retained generation", restored)
	}
	nodes, err := f.adapter.Nodes(t.Context())
	if err != nil || len(nodes) != 1 || nodes[0].Active != 1 || nodes[0].Retained != 1 {
		t.Fatal("restore reserved a second retained slot", nodes, err)
	}
}
