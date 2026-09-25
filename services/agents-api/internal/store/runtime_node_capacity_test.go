package store

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
)

func nodeCapacityPointer[T any](v T) *T { return &v }

func nodeCapacityHealth() RuntimeNodeHealth {
	return RuntimeNodeHealth{ProviderReady: true, CPUCount: nodeCapacityPointer(int64(256)), EffectiveCPUCores: nodeCapacityPointer(256.0), HostTotalMemoryBytes: nodeCapacityPointer(int64(1 << 40)), EffectiveMemoryBytes: nodeCapacityPointer(int64(1 << 40)), AvailableMemoryBytes: nodeCapacityPointer(int64(1 << 40)), AvailableDiskBytes: nodeCapacityPointer(int64(1 << 40)), ObservedAt: nodeCapacityPointer(time.Now())}
}

func TestRuntimeNodeCapacityPolicy(t *testing.T) {
	now := time.Now()
	spec := SandboxDeploymentTestSpec("docker")
	spec.Resources.MemoryMiB = 4096
	raw, _ := json.Marshal(spec)
	d := sqlc.RuntimeDeployment{ProviderKind: "docker", Mode: "nodes", Generation: 1, Specification: raw}
	base := RuntimeNode{RuntimeNodeHealth: RuntimeNodeHealth{ProviderReady: true, EffectiveCPUCores: nodeCapacityPointer(8.0), EffectiveMemoryBytes: nodeCapacityPointer(int64(32 << 30)), AvailableMemoryBytes: nodeCapacityPointer(int64(24 << 30)), ObservedAt: &now}, Online: true, AdmissionState: "pending_confirmation", MaxActive: 1, MaxRetained: 1, deploymentGeneration: 1, specificationDigest: spec.Digest("docker")}
	for _, test := range []struct {
		name        string
		change      func(*RuntimeNode, *sqlc.RuntimeDeployment)
		status      string
		limit       int
		schedulable bool
	}{
		{name: "effective_limits", status: "ready", limit: 3},
		{name: "fractional_cpu", change: func(n *RuntimeNode, _ *sqlc.RuntimeDeployment) { n.EffectiveCPUCores = nodeCapacityPointer(2.5) }, status: "blocked"},
		{name: "missing_effective_cpu", change: func(n *RuntimeNode, _ *sqlc.RuntimeDeployment) {
			n.EffectiveCPUCores = nil
			n.CPUCount = nodeCapacityPointer(int64(128))
		}, status: "checking"},
		{name: "missing_memory", change: func(n *RuntimeNode, _ *sqlc.RuntimeDeployment) { n.EffectiveMemoryBytes = nil }, status: "checking"},
		{name: "stale_sample", change: func(n *RuntimeNode, _ *sqlc.RuntimeDeployment) {
			n.ObservedAt = nodeCapacityPointer(now.Add(-time.Minute))
		}, status: "checking"},
		{name: "spec_mismatch", change: func(n *RuntimeNode, _ *sqlc.RuntimeDeployment) { n.specificationDigest = "different" }, status: "blocked"},
		{name: "maintenance", change: func(_ *RuntimeNode, d *sqlc.RuntimeDeployment) { d.Maintenance = true }, status: "blocked"},
		{name: "enabled_missing_new_measurements", change: func(n *RuntimeNode, _ *sqlc.RuntimeDeployment) {
			n.AdmissionState = "enabled"
			n.EffectiveCPUCores = nil
		}, status: "checking", schedulable: true},
		{name: "enabled_full", change: func(n *RuntimeNode, _ *sqlc.RuntimeDeployment) { n.AdmissionState = "enabled"; n.Active = 1 }, status: "ready", limit: 3},
	} {
		t.Run(test.name, func(t *testing.T) {
			n, dep := base, d
			if test.change != nil {
				test.change(&n, &dep)
			}
			got := enrichRuntimeNode(n, dep, now)
			if got.Capacity.Status != test.status || got.Schedulable != test.schedulable {
				t.Fatalf("capacity=%+v schedulable=%v", got.Capacity, got.Schedulable)
			}
			if test.limit == 0 {
				if got.Capacity.RecommendedMaxActive != nil || got.Capacity.SelectableMaxActive != nil {
					t.Fatal("unknown/blocked limit became numeric")
				}
			} else if got.Capacity.SelectableMaxActive == nil || *got.Capacity.SelectableMaxActive != test.limit {
				t.Fatalf("limit=%v", got.Capacity.SelectableMaxActive)
			}
			if got.SandboxSpec == nil || got.SandboxSpec.MemoryBytes != 4<<30 || got.Capacity.ReservedCPUCores != 1 || got.Capacity.ReservedMemoryBytes != int64(32<<30)/10 {
				if n.EffectiveMemoryBytes != nil {
					t.Fatalf("wrong units/reserves: %+v %+v", got.SandboxSpec, got.Capacity)
				}
			}
		})
	}
}

func TestRuntimeNodePendingJSONLimitsUnknown(t *testing.T) {
	for _, state := range []string{"pending_confirmation", "enabled"} {
		raw, err := json.Marshal(RuntimeNode{AdmissionState: state, MaxActive: 4, MaxRetained: 16})
		if err != nil {
			t.Fatal(err)
		}
		var got map[string]any
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatal(err)
		}
		if state == "pending_confirmation" {
			if got["max_active"] != nil || got["max_retained"] != nil {
				t.Fatal(string(raw))
			}
		} else if got["max_active"] != float64(4) || got["max_retained"] != float64(16) {
			t.Fatal(string(raw))
		}
	}
}
