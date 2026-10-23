package deployment

import (
	"context"
	"math"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/machineapi"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

// hostReservedMiB stays for the host and the node service; memory is never overcommitted beyond it.
const hostReservedMiB = 2048

// RecommendCapacity derives a node's limits from its host measurements and the
// size of one sandbox. It reports false when the host measurements cannot
// establish them.
//
// Agent sandboxes mostly wait on the model, so CPU is committed at twice the
// host's cores. Memory is not overcommitted, after hostReservedMiB. A provider
// that checkpoints retains suspended sandboxes on disk, so it keeps as many
// root disks as fit in half the free disk; one that does not retains none.
func RecommendCapacity(host NodeHealth, resources sandbox.Resources, checkpoint bool) (Capacity, bool) {
	cpus := host.CPUCount
	var cores float64
	switch {
	case host.Host != nil && host.Host.EffectiveCPUCores != nil:
		cores = *host.Host.EffectiveCPUCores
	case cpus != nil:
		cores = float64(*cpus)
	}
	if cores <= 0 || host.Host == nil || host.Host.TotalMemoryBytes == nil || resources.CPUs == 0 || resources.MemoryMiB == 0 {
		return Capacity{}, false
	}
	memoryMiB := *host.Host.TotalMemoryBytes>>20 - hostReservedMiB
	byCPU := int64(math.Floor(min(2*cores/float64(resources.CPUs), maxNodeCapacity)))
	active := int(max(min(byCPU, memoryMiB/int64(resources.MemoryMiB), maxNodeCapacity), 1))
	retained := active
	if checkpoint && host.AvailableDiskBytes != nil && resources.RootDiskMiB > 0 {
		retained = int(min(max(int64(active), *host.AvailableDiskBytes>>20/2/int64(resources.RootDiskMiB)), maxNodeCapacity))
	}
	return Capacity{MaxActive: active, MaxRetained: retained}, true
}

// recommendations returns, for the current specification, the recommendation
// for a node's latest health: nil when there is no specification or the node
// reported too little.
func (s *Service) recommendations(ctx context.Context) (func(NodeHealth) *Capacity, error) {
	// The snapshot reads the deployment without opening its sealed credential.
	snapshot, err := s.reader.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	d := snapshot.Record
	spec, err := s.specification(d)
	if err != nil {
		return func(NodeHealth) *Capacity { return nil }, nil
	}
	checkpoint, err := s.registry.SupportsCheckpoint(d.Provider)
	if err != nil {
		return func(NodeHealth) *Capacity { return nil }, nil
	}
	return func(health NodeHealth) *Capacity {
		capacity, ok := RecommendCapacity(health, spec.Resources, checkpoint)
		if !ok {
			return nil
		}
		return &capacity
	}, nil
}

// enrollmentHealth is the host an enrollment reports, validated like a health report.
func enrollmentHealth(e machineapi.NodeEnrollment) (NodeHealth, error) {
	observed := time.Now()
	return normalizeHealth(NodeHealth{CPUCount: e.CPUCount, AvailableDiskBytes: e.AvailableDiskBytes, Host: &NodeHost{EffectiveCPUCores: e.EffectiveCPUCores, TotalMemoryBytes: e.TotalMemoryBytes, AvailableDiskBytes: e.AvailableDiskBytes, ObservedAt: &observed}})
}
