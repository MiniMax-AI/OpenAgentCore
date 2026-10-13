package deploymentpg_test

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment/placement"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/google/uuid"
)

func qualifyPressureRetention(t *testing.T, f fixture, owner deployment.Allocation) deployment.Allocation {
	t.Helper()
	config := uuid.NewString()
	for _, statement := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO workspace_fs_configurations(id,adapter,parameters) VALUES($1,'fixture','{}')`, []any{config}},
		{`INSERT INTO environment_workspaces(object_id,environment_id,configuration_id,state,attachment) VALUES($1,$2,$3,'ready','{}')`, []any{uuid.NewString(), owner.EnvironmentID, config}},
		{`UPDATE devices SET supported_agent_kinds='[{"kind":"codex","available":true,"capabilities":{"retained_native_history":true}}]' WHERE id=$1`, []any{owner.DeviceID}},
		{`UPDATE runtime_allocations SET compute_phase='suspended',compute_state='{}',compute_retained_until=clock_timestamp()+interval '24 hours' WHERE id=$1`, []any{owner.ID}},
	} {
		if _, err := f.pool.Exec(t.Context(), statement.sql, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	current, err := f.adapter.EnvironmentAllocation(t.Context(), owner.Key())
	if err != nil {
		t.Fatal(err)
	}
	return current
}

func TestRetentionPressureKeepsHistoryUntilOrdinaryCleanupSettles(t *testing.T) {
	f := newFixture(t)
	changes, _ := f.execution(t)
	installation, view := f.initialize(t, changes, sandbox.Selection{Provider: "microsandbox", DeploymentSpec: retainedSpecification()})
	node := f.enroll(t, view, deployment.Capacity{MaxActive: 3, MaxRetained: 3})
	f.connect(t, node.NodeID)
	owner := qualifyPressureRetention(t, f, runningPressureOwner(t, f, changes, installation, node.NodeID, view.Generation))
	for range 2 {
		qualifyPressureRetention(t, f, runningPressureOwner(t, f, changes, installation, node.NodeID, view.Generation))
	}
	waiting := pendingPressureDemand(t, f)
	for range 6 {
		pendingPressureDemand(t, f)
	}
	if _, err := changes.EnsurePlacement(t.Context(), waiting, installation); !errors.Is(err, placement.ErrNodeUnavailable) {
		t.Fatal("reserved despite retained capacity limit")
	}
	current, err := f.adapter.EnvironmentAllocation(t.Context(), owner.Key())
	if err != nil || current.Expired || !current.ComputeRetainedUntil.Equal(*owner.ComputeRetainedUntil) {
		t.Fatal("pressure shortened retention", current, err)
	}
	// Advance the fixture clock past the authored deadline, not because of demand.
	if _, err := f.pool.Exec(t.Context(), `UPDATE runtime_allocations SET compute_retained_until=clock_timestamp()-interval '1 second' WHERE id=$1`, owner.ID); err != nil {
		t.Fatal(err)
	}
	// The database deadline is only an intent. It is not a cleanup receipt.
	nodes, err := f.adapter.Nodes(t.Context())
	if err != nil || nodes[0].Retained != 3 || nodes[0].Active != 0 {
		t.Fatal("released capacity before cleanup", nodes, err)
	}
	if _, err = changes.EnsurePlacement(t.Context(), waiting, installation); !errors.Is(err, placement.ErrNodeUnavailable) {
		t.Fatal("reserved before cleanup")
	}
	current, err = f.adapter.EnvironmentAllocation(t.Context(), owner.Key())
	if err != nil || !current.Expired {
		t.Fatal("database expiry", current, err)
	}
	pending, err := changes.RequestCleanup(t.Context(), current)
	if err != nil {
		t.Fatal(err)
	}
	var status, filesystem string
	if err = f.pool.QueryRow(t.Context(), `SELECT e.status,w.state FROM environments e JOIN environment_workspaces w ON w.environment_id=e.id WHERE e.id=$1`, owner.EnvironmentID).Scan(&status, &filesystem); err != nil || status == "expired" || status == "failed" || filesystem != "ready" {
		t.Fatal("lost retained environment", status, filesystem, err)
	}
	// This fixture supplies the native cleanup completion that a real lifecycle
	// must obtain before invoking ReleaseAllocation.
	if _, err = changes.ReleaseAllocation(t.Context(), pending); err != nil {
		t.Fatal(err)
	}
	if _, err = changes.EnsurePlacement(t.Context(), waiting, installation); err != nil {
		t.Fatal("waiting demand did not progress", err)
	}
}

func TestPressurePreservesRetainableOwnerWhenRetainedSlotsAreFull(t *testing.T) {
	f := newFixture(t)
	changes, _ := f.execution(t)
	installation, view := f.initialize(t, changes, sandbox.Selection{Provider: "microsandbox", DeploymentSpec: retainedSpecification()})
	node := f.enroll(t, view, deployment.Capacity{MaxActive: 1, MaxRetained: 1})
	f.connect(t, node.NodeID)
	owner := qualifyPressureRetention(t, f, runningPressureOwner(t, f, changes, installation, node.NodeID, view.Generation))
	if _, err := f.pool.Exec(t.Context(), `UPDATE runtime_allocations SET compute_phase='running',compute_retained_until=NULL WHERE id=$1`, owner.ID); err != nil {
		t.Fatal(err)
	}
	owner, err := f.adapter.EnvironmentAllocation(t.Context(), owner.Key())
	if err != nil {
		t.Fatal(err)
	}
	pendingPressureDemand(t, f)
	until := time.Now().Add(24 * time.Hour)
	if _, err = changes.SetCompute(t.Context(), owner, "quiescing", json.RawMessage(`{}`), &until); !errors.Is(err, deployment.ErrNotIdle) {
		t.Fatal("suspended without usable capacity", err)
	}
}
