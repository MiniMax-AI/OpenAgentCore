package deploymentpg_test

import (
	"errors"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment/placement"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

func TestPlacementDemandAdmitsOnlyAvailableCompute(t *testing.T) {
	f := newFixture(t)
	changes, _ := f.execution(t)
	installation, view := f.initialize(t, changes, sandbox.Selection{Provider: "microsandbox", DeploymentSpec: retainedSpecification()})
	node := f.enroll(t, view, deployment.Capacity{MaxActive: 3, MaxRetained: 10})
	f.connect(t, node.NodeID)
	keys := make([]deployment.AllocationKey, 10)
	for i := range keys {
		keys[i] = pendingHostedEnvironment(t, f)
		if i%2 == 0 {
			if _, err := f.pool.Exec(t.Context(), `UPDATE environments SET initialization='complete' WHERE id=$1`, keys[i].EnvironmentID); err != nil {
				t.Fatal(err)
			}
		}
	}
	demand, _, err := f.adapter.PlacementDemand(t.Context(), deployment.PlacementDemandCursor{})
	if err != nil || len(demand) != 10 {
		t.Fatal(demand, err)
	}
	for i, request := range demand {
		_, err := changes.EnsurePlacement(t.Context(), deployment.AllocationKey{TenantID: request.TenantID, EnvironmentID: request.ID}, installation)
		if i < 3 && err != nil {
			t.Fatal(i, err)
		}
		if i >= 3 && !errors.Is(err, placement.ErrNodeUnavailable) {
			t.Fatal(i, err)
		}
	}
	var reserved, devices, allocations int
	if err := f.pool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM runtime_placements WHERE released_at IS NULL),(SELECT count(*) FROM devices),(SELECT count(*) FROM runtime_allocations)`).Scan(&reserved, &devices, &allocations); err != nil {
		t.Fatal(err)
	}
	if reserved != 3 || devices != 0 || allocations != 0 {
		t.Fatal(reserved, devices, allocations)
	}
	// A pending node request must never enter the direct Provider's unallocated lane.
	direct, err := f.adapter.UnallocatedEnvironments(t.Context(), "", "")
	if err != nil || len(direct) != 0 {
		t.Fatal("node demand entered direct lane", direct, err)
	}
	remaining, _, err := f.adapter.PlacementDemand(t.Context(), deployment.PlacementDemandCursor{})
	if err != nil || len(remaining) != 7 {
		t.Fatal(remaining, err)
	}
	// A retried reservation consumes no additional slot or identity.
	first := demand[0]
	if _, err := changes.EnsurePlacement(t.Context(), deployment.AllocationKey{TenantID: first.TenantID, EnvironmentID: first.ID}, installation); err != nil {
		t.Fatal(err)
	}
}

func TestPlacementDemandPagesByOriginalDemandTime(t *testing.T) {
	f := newFixture(t)
	changes, _ := f.execution(t)
	f.initialize(t, changes, sandbox.Selection{Provider: "microsandbox", DeploymentSpec: retainedSpecification()})
	at := time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)
	for i := 0; i < 40; i++ {
		key := pendingHostedEnvironment(t, f)
		// UUIDs are random; demand order must follow durable acceptance time.
		if _, err := f.pool.Exec(t.Context(), `UPDATE sessions SET created_at=$2 WHERE id=(SELECT session_id FROM environments WHERE id=$1)`, key.EnvironmentID, at.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	first, next, err := f.adapter.PlacementDemand(t.Context(), deployment.PlacementDemandCursor{})
	if err != nil || len(first) != 32 {
		t.Fatal(len(first), err)
	}
	for i, item := range first {
		if !item.At.Equal(at.Add(time.Duration(i) * time.Second)) {
			t.Fatal(i, item.At)
		}
	}
	second, end, err := f.adapter.PlacementDemand(t.Context(), next)
	if err != nil || len(second) != 8 {
		t.Fatal(len(second), err)
	}
	if end.EnvironmentID != "" || !end.Until.Equal(next.Until) {
		t.Fatal("short page lost its scan horizon", end, next)
	}
	if !second[0].At.Equal(at.Add(32 * time.Second)) {
		t.Fatal(second[0])
	}
}

func TestFirstPlacementChecksActualGenerationHistorySupport(t *testing.T) {
	f := newFixture(t)
	changes, _ := f.execution(t)
	installation, view := f.initialize(t, changes, sandbox.Selection{Provider: "microsandbox", DeploymentSpec: retainedSpecification()})
	node := f.enroll(t, view, deployment.Capacity{MaxActive: 1, MaxRetained: 1})
	f.connect(t, node.NodeID)
	key := pendingHostedEnvironment(t, f)
	if _, err := f.pool.Exec(t.Context(), `UPDATE sessions SET engine='mcode' WHERE id=(SELECT session_id FROM environments WHERE id=$1)`, key.EnvironmentID); err != nil {
		t.Fatal(err)
	}
	if _, err := changes.EnsurePlacement(t.Context(), key, installation); !errors.Is(err, placement.ErrNodeUnavailable) {
		t.Fatal("unsupported Harness reserved external filesystem", err)
	}
	pending, _, err := f.adapter.PlacementDemand(t.Context(), deployment.PlacementDemandCursor{})
	if err != nil || len(pending) != 1 {
		t.Fatal(pending, err)
	}
	if _, err := f.pool.Exec(t.Context(), `UPDATE sessions SET engine='codex' WHERE id=(SELECT session_id FROM environments WHERE id=$1)`, key.EnvironmentID); err != nil {
		t.Fatal(err)
	}
	if _, err := changes.EnsurePlacement(t.Context(), key, installation); err != nil {
		t.Fatal(err)
	}
	// Terminal state is rechecked under the Session lock, after the demand read.
	another := pendingHostedEnvironment(t, f)
	if _, err := f.pool.Exec(t.Context(), `UPDATE environments SET status='expired' WHERE id=$1`, another.EnvironmentID); err != nil {
		t.Fatal(err)
	}
	if _, err := changes.EnsurePlacement(t.Context(), another, installation); !errors.Is(err, deployment.ErrAllocationConflict) {
		t.Fatal(err)
	}
}

func pendingHostedEnvironment(t *testing.T, f fixture) deployment.AllocationKey {
	t.Helper()
	key := hostedEnvironment(t, f.pool)
	if _, err := f.pool.Exec(t.Context(), `UPDATE environments SET initialization='pending' WHERE id=$1`, key.EnvironmentID); err != nil {
		t.Fatal(err)
	}
	return key
}

func TestReleasedFileWakeUsesFirstDemandAndLatestOwner(t *testing.T) {
	f := newFixture(t)
	changes, _ := f.execution(t)
	installation, view := f.initialize(t, changes, sandbox.Selection{Provider: "microsandbox", DeploymentSpec: retainedSpecification()})
	node := f.enroll(t, view, deployment.Capacity{MaxActive: 1, MaxRetained: 2})
	f.connect(t, node.NodeID)
	key := hostedEnvironment(t, f.pool)
	if _, err := changes.EnsurePlacement(t.Context(), key, installation); err != nil {
		t.Fatal(err)
	}
	owner, err := changes.ReserveAllocation(t.Context(), key, installation, credentialHash())
	if err != nil {
		t.Fatal(err)
	}
	owner = releaseRetainedFixture(t, f, changes, owner)
	if err := f.service.TouchActivity(t.Context(), key.TenantID, key.EnvironmentID); err != nil {
		t.Fatal(err)
	}
	first, _, err := f.adapter.PlacementDemand(t.Context(), deployment.PlacementDemandCursor{})
	if err != nil || len(first) != 1 || !first[0].Retained {
		t.Fatal(first, err)
	}
	if err := f.service.TouchActivity(t.Context(), key.TenantID, key.EnvironmentID); err != nil {
		t.Fatal(err)
	}
	replay, _, err := f.adapter.PlacementDemand(t.Context(), deployment.PlacementDemandCursor{})
	if err != nil || len(replay) != 1 || !replay[0].At.Equal(first[0].At) {
		t.Fatal(replay, err)
	}
	if _, err := changes.EnsurePlacement(t.Context(), key, installation); err != nil {
		t.Fatal(err)
	}
	next, err := changes.ReserveAllocation(t.Context(), key, installation, credentialHash())
	if err != nil {
		t.Fatal(err)
	}
	if next.ID == owner.ID {
		t.Fatal("wake reused released owner")
	}
	releaseRetainedFixture(t, f, changes, next)
	idle, _, err := f.adapter.PlacementDemand(t.Context(), deployment.PlacementDemandCursor{})
	if err != nil || len(idle) != 0 {
		t.Fatal("historical wake leaked into later owner", idle, err)
	}
}

func TestPlacementDemandWrapsDespiteContinuousNewArrivals(t *testing.T) {
	f := newFixture(t)
	changes, _ := f.execution(t)
	f.initialize(t, changes, sandbox.Selection{Provider: "microsandbox", DeploymentSpec: retainedSpecification()})
	add := func(n int) {
		for range n {
			pendingHostedEnvironment(t, f)
		}
	}
	add(64)
	first, next, err := f.adapter.PlacementDemand(t.Context(), deployment.PlacementDemandCursor{})
	if err != nil || len(first) != 32 || next.Until.IsZero() {
		t.Fatal(len(first), next, err)
	}
	horizon := next.Until
	add(64)
	second, next, err := f.adapter.PlacementDemand(t.Context(), next)
	if err != nil || len(second) != 32 || !next.Until.Equal(horizon) {
		t.Fatal(len(second), next, err)
	}
	add(64)
	end, next, err := f.adapter.PlacementDemand(t.Context(), next)
	if err != nil || len(end) != 0 || next != (deployment.PlacementDemandCursor{}) {
		t.Fatal(len(end), next, err)
	}
	// Still-unplaced old demand is retried before the subsequent arrivals.
	again, _, err := f.adapter.PlacementDemand(t.Context(), next)
	if err != nil || len(again) != 32 || again[0].ID != first[0].ID {
		t.Fatal(again, err)
	}
}
