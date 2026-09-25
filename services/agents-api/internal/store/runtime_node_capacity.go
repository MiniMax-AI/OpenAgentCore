package store

import (
	"math"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
)

const runtimeNodeReserveCPU = 1.0
const runtimeNodeMinimumReserveMemory int64 = 2 << 30
const runtimeNodeDefaultRetained = 16

type RuntimeNodeHost struct {
	EffectiveCPUCores    *float64   `json:"effective_cpu_cores"`
	TotalMemoryBytes     *int64     `json:"total_memory_bytes"`
	EffectiveMemoryBytes *int64     `json:"effective_memory_bytes"`
	AvailableMemoryBytes *int64     `json:"available_memory_bytes"`
	AvailableDiskBytes   *int64     `json:"available_disk_bytes"`
	ObservedAt           *time.Time `json:"observed_at"`
}
type RuntimeNodeSandboxSpec struct {
	CPUCores                  uint32 `json:"cpu_cores"`
	MemoryBytes               int64  `json:"memory_bytes"`
	RootDiskLimitBytes        *int64 `json:"root_disk_limit_bytes"`
	EnvironmentDiskLimitBytes *int64 `json:"environment_disk_limit_bytes"`
	RuntimeVersion            string `json:"runtime_version"`
}
type RuntimeNodeCapacity struct {
	Status               string   `json:"status"`
	RecommendedMaxActive *int     `json:"recommended_max_active"`
	SelectableMaxActive  *int     `json:"selectable_max_active"`
	ReservedCPUCores     float64  `json:"reserved_cpu_cores"`
	ReservedMemoryBytes  int64    `json:"reserved_memory_bytes"`
	Reasons              []string `json:"reasons"`
}

func runtimeNodeMatchesDeployment(generation int64, digest string, d sqlc.RuntimeDeployment) bool {
	spec, err := deploymentSpecification(d)
	return err == nil && d.Mode == "nodes" && generation == d.Generation && digest == spec.Digest(d.ProviderKind)
}

func enrichRuntimeNode(n RuntimeNode, d sqlc.RuntimeDeployment, now time.Time) RuntimeNode {
	n.Host = &RuntimeNodeHost{n.EffectiveCPUCores, n.HostTotalMemoryBytes, n.EffectiveMemoryBytes, n.AvailableMemoryBytes, n.AvailableDiskBytes, n.ObservedAt}
	n.Capacity = RuntimeNodeCapacity{Status: "checking", ReservedCPUCores: runtimeNodeReserveCPU, ReservedMemoryBytes: runtimeNodeMinimumReserveMemory, Reasons: []string{}}
	if n.EffectiveMemoryBytes != nil {
		n.Capacity.ReservedMemoryBytes = max(runtimeNodeMinimumReserveMemory, *n.EffectiveMemoryBytes/10)
	}
	matches := runtimeNodeMatchesDeployment(n.deploymentGeneration, n.specificationDigest, d)
	n.Schedulable = n.AdmissionState == "enabled" && !d.Maintenance && matches && n.Online && n.ProviderReady && n.Active < int64(n.MaxActive) && n.Retained < int64(n.MaxRetained)
	spec, err := deploymentSpecification(d)
	if err != nil || d.Mode != "nodes" || spec.Runtime == nil {
		n.Capacity.Status = "blocked"
		n.Capacity.Reasons = append(n.Capacity.Reasons, "specification_unavailable")
		return n
	}
	n.SandboxSpec = &RuntimeNodeSandboxSpec{CPUCores: spec.Resources.CPUs, MemoryBytes: int64(spec.Resources.MemoryMiB) << 20, RuntimeVersion: spec.Runtime.SourceCommit}
	if spec.Resources.RootDiskMiB > 0 {
		v := int64(spec.Resources.RootDiskMiB) << 20
		n.SandboxSpec.RootDiskLimitBytes = &v
	}
	if spec.Resources.EnvironmentDiskMiB > 0 {
		v := int64(spec.Resources.EnvironmentDiskMiB) << 20
		n.SandboxSpec.EnvironmentDiskLimitBytes = &v
	}
	if !matches {
		n.Capacity.Status = "blocked"
		n.Capacity.Reasons = append(n.Capacity.Reasons, "specification_mismatch")
		return n
	}
	if d.Maintenance {
		n.Capacity.Status = "blocked"
		n.Capacity.Reasons = append(n.Capacity.Reasons, "deployment_maintenance")
		return n
	}
	if !n.Online || !n.ProviderReady {
		n.Capacity.Reasons = append(n.Capacity.Reasons, "node_unavailable")
		return n
	}
	if n.ObservedAt == nil || n.ObservedAt.Before(now.Add(-45*time.Second)) || n.ObservedAt.After(now.Add(30*time.Second)) {
		n.Capacity.Reasons = append(n.Capacity.Reasons, "observation_stale")
		return n
	}
	if n.EffectiveCPUCores == nil || n.EffectiveMemoryBytes == nil || *n.EffectiveCPUCores <= 0 || math.IsNaN(*n.EffectiveCPUCores) || math.IsInf(*n.EffectiveCPUCores, 0) || *n.EffectiveMemoryBytes <= 0 {
		n.Capacity.Reasons = append(n.Capacity.Reasons, "host_capacity_unknown")
		return n
	}
	cpu := math.Floor((*n.EffectiveCPUCores - runtimeNodeReserveCPU) / float64(n.SandboxSpec.CPUCores))
	memory := (*n.EffectiveMemoryBytes - n.Capacity.ReservedMemoryBytes) / n.SandboxSpec.MemoryBytes
	limit := min(cpu, float64(memory), 1000000)
	if limit < 1 {
		n.Capacity.Status = "blocked"
		n.Capacity.Reasons = append(n.Capacity.Reasons, "insufficient_host_capacity")
		return n
	}
	selected := int(limit)
	n.Capacity.Status = "ready"
	n.Capacity.RecommendedMaxActive = &selected
	n.Capacity.SelectableMaxActive = &selected
	if reason := runtimeNodeFreeCapacityReason(n); reason != "" {
		n.Capacity.Reasons = append(n.Capacity.Reasons, reason)
		if n.AdmissionState != "enabled" {
			n.Capacity.Status = "blocked"
			n.Capacity.RecommendedMaxActive = nil
			n.Capacity.SelectableMaxActive = nil
		}
	}
	if n.AvailableDiskBytes != nil && n.SandboxSpec.RootDiskLimitBytes != nil && n.SandboxSpec.EnvironmentDiskLimitBytes != nil && *n.AvailableDiskBytes < *n.SandboxSpec.RootDiskLimitBytes+*n.SandboxSpec.EnvironmentDiskLimitBytes {
		n.Capacity.Reasons = append(n.Capacity.Reasons, "low_observed_disk_space")
	}
	return n
}

func runtimeNodeFreeCapacityReason(n RuntimeNode) string {
	if n.SandboxSpec == nil {
		return "specification_unavailable"
	}
	if n.AvailableMemoryBytes == nil {
		return "available_memory_unknown"
	}
	if *n.AvailableMemoryBytes < n.SandboxSpec.MemoryBytes+n.Capacity.ReservedMemoryBytes {
		return "insufficient_available_memory"
	}

	return ""
}
