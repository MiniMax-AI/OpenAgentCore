package docker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimeobs"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
)

var _ runtimeobs.Source = (*Provider)(nil)

func (*Provider) ObservationProviderType() string { return "docker" }

// Observe is read-only. Inspect verifies allocation ownership before Docker
// statistics are requested; it never renews or changes the container.
func (p *Provider) Observe(ctx context.Context, target runtimeobs.Target) (runtimeobs.Sample, error) {
	if target.Mode != runtimeobs.ModeManaged || target.Instance.AllocationID == "" {
		return runtimeobs.Sample{}, sandbox.ErrInvalid
	}
	if target.Instance.ProviderKey != p.config.InstallationID {
		return runtimeobs.Sample{}, sandbox.ErrOwnership
	}
	reference := sandbox.Reference{TenantID: target.TenantID, EnvironmentID: target.EnvironmentID, AllocationID: target.Instance.AllocationID}
	inspected, err := p.inspect(ctx, reference)
	if errors.Is(err, sandbox.ErrNotFound) {
		return runtimeobs.Sample{}, runtimeobs.ErrNotRunning
	}
	if err != nil {
		return runtimeobs.Sample{}, err
	}
	if inspected.Container.State == nil || !inspected.Container.State.Running {
		return runtimeobs.Sample{}, runtimeobs.ErrNotRunning
	}
	result, err := p.client.ContainerStats(ctx, inspected.Container.ID, client.ContainerStatsOptions{Stream: false, IncludePreviousSample: false})
	if err != nil {
		return runtimeobs.Sample{}, fmt.Errorf("read Docker Runtime statistics: %w", err)
	}
	defer result.Body.Close()
	var stats dockerStatsResponse
	if err := json.NewDecoder(result.Body).Decode(&stats); err != nil {
		return runtimeobs.Sample{}, fmt.Errorf("decode Docker Runtime statistics: %w", err)
	}
	return sampleFromDocker(inspected.Container, stats)
}

type dockerStatsResponse struct {
	Read     time.Time `json:"read"`
	CPUStats *struct {
		CPUUsage *struct {
			TotalUsage *uint64 `json:"total_usage"`
		} `json:"cpu_usage"`
	} `json:"cpu_stats"`
	MemoryStats *struct {
		Usage *uint64 `json:"usage"`
	} `json:"memory_stats"`
}

func sampleFromDocker(inspected container.InspectResponse, stats dockerStatsResponse) (runtimeobs.Sample, error) {
	if inspected.State == nil || inspected.HostConfig == nil || !inspected.State.Running || stats.Read.IsZero() {
		return runtimeobs.Sample{}, errors.New("incomplete Docker Runtime observation")
	}
	startedAt, err := time.Parse(time.RFC3339Nano, inspected.State.StartedAt)
	if err != nil || startedAt.IsZero() || startedAt.After(stats.Read) {
		return runtimeobs.Sample{}, errors.New("invalid Docker Runtime start time")
	}
	sample := runtimeobs.Sample{
		ObservedAt: stats.Read,
		StartedAt:  &startedAt,
	}
	if stats.CPUStats != nil && stats.CPUStats.CPUUsage != nil && stats.CPUStats.CPUUsage.TotalUsage != nil {
		cpuUsage := float64(*stats.CPUStats.CPUUsage.TotalUsage) / float64(time.Second)
		sample.CPUUsageSecondsTotal = &cpuUsage
	}
	if stats.MemoryStats != nil && stats.MemoryStats.Usage != nil {
		memoryUsage := *stats.MemoryStats.Usage
		sample.MemoryUsageBytes = &memoryUsage
	}
	if inspected.HostConfig.NanoCPUs > 0 {
		capacity := float64(inspected.HostConfig.NanoCPUs) / 1_000_000_000
		sample.CPUCapacityCores = &capacity
	}
	if inspected.HostConfig.Memory > 0 {
		limit := uint64(inspected.HostConfig.Memory)
		sample.MemoryLimitBytes = &limit
	}
	return sample, nil
}
