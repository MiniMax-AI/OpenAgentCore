package microsandbox

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimeobs"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

var _ runtimeobs.Source = (*Provider)(nil)

func (*Provider) ObservationProviderType() string { return "microsandbox" }

// Observe reads one point-in-time native metrics snapshot through the existing
// one-shot helper. The persisted compute receipt selects the exact generation;
// browser input and provider display names never select a sandbox.
func (p *Provider) Observe(ctx context.Context, target runtimeobs.Target) (runtimeobs.Sample, error) {
	if target.Mode != runtimeobs.ModeManaged || target.Instance.AllocationID == "" {
		return runtimeobs.Sample{}, sandbox.ErrInvalid
	}
	if target.Instance.ProviderKey != p.config.InstallationID {
		return runtimeobs.Sample{}, sandbox.ErrOwnership
	}
	reference := sandbox.Reference{TenantID: target.TenantID, EnvironmentID: target.EnvironmentID, AllocationID: target.Instance.AllocationID}
	if target.Instance.ComputePhase == "suspended" {
		return runtimeobs.Sample{}, runtimeobs.ErrNotRunning
	}
	var state struct {
		Current Compute `json:"current"`
	}
	if json.Unmarshal(target.Instance.ProviderState, &state) != nil || state.Current.ID == "" {
		// Suspension-disabled allocations predate durable compute receipts. A
		// deterministic name is not an incarnation fence, so do not sample it.
		if target.Instance.ComputePhase == "disabled" {
			return runtimeobs.Sample{}, runtimeobs.ErrUnavailable
		}
		return runtimeobs.Sample{}, sandbox.ErrOwnership
	}
	if ValidateCompute(p.config, reference, state.Current) != nil {
		return runtimeobs.Sample{}, sandbox.ErrOwnership
	}
	out, err := p.call(ctx, Request{Operation: "metrics", Reference: reference, Compute: state.Current})
	if errors.Is(err, sandbox.ErrNotFound) {
		return runtimeobs.Sample{}, runtimeobs.ErrNotRunning
	}
	if err != nil {
		return runtimeobs.Sample{}, err
	}
	if out.Metrics == nil {
		return runtimeobs.Sample{}, ErrUnconfirmed
	}
	return sampleFromMetrics(p.config, *out.Metrics)
}

func sampleFromMetrics(config Config, metrics Metrics) (runtimeobs.Sample, error) {
	if metrics.ObservedAt.IsZero() || metrics.Uptime < 0 || metrics.MemoryLimitBytes == 0 || config.CPUs == 0 {
		return runtimeobs.Sample{}, ErrUnconfirmed
	}
	// Both values come from one native registry sample at millisecond precision.
	// Helper wall time and the SDK's rounded uptime cannot establish this fence.
	startedAt := metrics.ObservedAt.Add(-metrics.Uptime)
	if startedAt.Unix() < 0 || startedAt.After(metrics.ObservedAt) {
		return runtimeobs.Sample{}, ErrUnconfirmed
	}
	cpuUsage := float64(metrics.VCPUTimeNs) / float64(time.Second)
	cpuCapacity := float64(config.CPUs)
	memoryUsage, memoryLimit := metrics.MemoryBytes, metrics.MemoryLimitBytes
	return runtimeobs.Sample{
		ObservedAt: metrics.ObservedAt, StartedAt: &startedAt,
		CPUUsageSecondsTotal: &cpuUsage, CPUCapacityCores: &cpuCapacity,
		MemoryUsageBytes: &memoryUsage, MemoryLimitBytes: &memoryLimit,
	}, nil
}
