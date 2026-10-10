package deploymentpg_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
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
	node := f.enroll(t, view, deployment.Capacity{MaxActive: 1, MaxRetained: 1})
	f.connect(t, node.NodeID)
	owner := qualifyPressureRetention(t, f, runningPressureOwner(t, f, changes, installation, node.NodeID, view.Generation))
	waiting := pendingPressureDemand(t, f)
	ended, err := changes.EndRetentionForDemand(t.Context(), owner)
	if err != nil {
		t.Fatal(err)
	}
	if ended.ComputeRetainedUntil == nil || !ended.ComputeRetainedUntil.Before(*owner.ComputeRetainedUntil) {
		t.Fatal("retention was not ended", ended)
	}
	// The database deadline is only an intent. It is not a cleanup receipt.
	nodes, err := f.adapter.Nodes(t.Context())
	if err != nil || nodes[0].Retained != 1 {
		t.Fatal("released capacity before cleanup", nodes, err)
	}
	if _, err = changes.EnsurePlacement(t.Context(), waiting, installation); err == nil {
		t.Fatal("reserved before cleanup")
	}
	current, err := f.adapter.EnvironmentAllocation(t.Context(), owner.Key())
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

func TestRetentionPressureGuards(t *testing.T) {
	for _, name := range []string{"no demand", "retained headroom", "wake requested", "missing native history", "free other node", "already ending"} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			changes, _ := f.execution(t)
			installation, view := f.initialize(t, changes, sandbox.Selection{Provider: "microsandbox", DeploymentSpec: retainedSpecification()})
			limit := 1
			if name == "retained headroom" || name == "already ending" {
				limit = 2
			}
			node := f.enroll(t, view, deployment.Capacity{MaxActive: 1, MaxRetained: limit})
			f.connect(t, node.NodeID)
			owner := qualifyPressureRetention(t, f, runningPressureOwner(t, f, changes, installation, node.NodeID, view.Generation))
			if name != "no demand" {
				pendingPressureDemand(t, f)
			}
			var err error
			switch name {
			case "wake requested":
				_, err = f.pool.Exec(t.Context(), `UPDATE runtime_allocations SET compute_wake_requested=true WHERE id=$1`, owner.ID)
			case "missing native history":
				_, err = f.pool.Exec(t.Context(), `UPDATE devices SET supported_agent_kinds='[]' WHERE id=$1`, owner.DeviceID)
			case "free other node":
				other := f.enroll(t, view, deployment.Capacity{MaxActive: 1, MaxRetained: 2})
				f.connect(t, other.NodeID)
			case "already ending":
				other := qualifyPressureRetention(t, f, runningPressureOwner(t, f, changes, installation, node.NodeID, view.Generation))
				_, err = f.pool.Exec(t.Context(), `UPDATE runtime_allocations SET compute_retained_until=clock_timestamp()-interval '1 second' WHERE id=$1`, other.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			next, err := changes.EndRetentionForDemand(t.Context(), owner)
			if err != nil {
				t.Fatal(err)
			}
			if next.ComputeRevision != owner.ComputeRevision || next.ComputeRetainedUntil == nil || !next.ComputeRetainedUntil.Equal(*owner.ComputeRetainedUntil) {
				t.Fatal("ended retention despite guard", next)
			}
		})
	}
}

func TestPressureSuspendsRetainableOwnerWhenRetainedSlotsAreFull(t *testing.T) {
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
	if _, err = changes.SetCompute(t.Context(), owner, "quiescing", json.RawMessage(`{}`), &until, 5*time.Minute); err != nil {
		t.Fatal(err)
	}
}
