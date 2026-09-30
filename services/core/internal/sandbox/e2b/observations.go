package e2b

import (
	"context"
	"math"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimeobs"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

var (
	_ runtimeobs.Source      = (*Provider)(nil)
	_ runtimeobs.BatchSource = (*Provider)(nil)
)

// maxClockLead tolerates an E2B metrics timestamp slightly ahead of Core's
// clock. A larger lead is treated as an unavailable sample, never as fresh data.
const maxClockLead = 30 * time.Second

// Observation is one allocation's latest E2B metrics point. Status is
// observed, not_running, unavailable or ownership; only observed carries
// values. A malformed E2B point is unavailable for its row only. Values keep E2B's units: CPUUsedPct is a percentage of all
// CPUCount cores, and memory and disk are in bytes.
type Observation struct {
	sandbox.Reference
	Status                string
	ObservedAt, StartedAt *time.Time `json:",omitempty"`
	CPUCount, CPUUsedPct  *float64   `json:",omitempty"`
	MemUsed, MemTotal     *uint64    `json:",omitempty"`
	DiskUsed, DiskTotal   *uint64    `json:",omitempty"`
}

func (*Provider) ObservationProviderType() string { return "e2b" }

// Observe reads one allocation through the same helper request as ObserveBatch.
func (p *Provider) Observe(ctx context.Context, target runtimeobs.Target) (runtimeobs.Sample, error) {
	results, _ := p.ObserveBatch(ctx, []runtimeobs.Target{target})
	return results[0].Sample, results[0].Err
}

// ObserveBatch reads up to runtimeobs.MaxBatchTargets allocations with one
// helper request. The helper takes sandbox IDs from its private receipts,
// confirms each running sandbox by its allocation labels and reads E2B's batch
// metrics once. It never connects to, renews or changes a sandbox.
func (p *Provider) ObserveBatch(ctx context.Context, targets []runtimeobs.Target) ([]runtimeobs.BatchResult, error) {
	results := make([]runtimeobs.BatchResult, len(targets))
	references := make([]sandbox.Reference, 0, len(targets))
	positions := make([]int, 0, len(targets))
	for index, target := range targets {
		reference := sandbox.Reference{TenantID: target.TenantID, EnvironmentID: target.EnvironmentID, AllocationID: target.Instance.AllocationID}
		switch {
		case target.Mode != runtimeobs.ModeManaged || !validReference(reference) || len(targets) > runtimeobs.MaxBatchTargets:
			results[index].Err = sandbox.ErrInvalid
		case target.Instance.ProviderKey != p.config.InstallationID:
			results[index].Err = sandbox.ErrOwnership
		default:
			references = append(references, reference)
			positions = append(positions, index)
		}
	}
	if len(references) == 0 {
		return results, nil
	}
	observations, err := p.observe(ctx, references)
	now := p.now()
	for offset, index := range positions {
		if err != nil {
			results[index].Err = err
			continue
		}
		results[index].Sample, results[index].Err = sampleFromObservation(observations[offset], now)
	}
	return results, nil
}

func (p *Provider) observe(ctx context.Context, references []sandbox.Reference) ([]Observation, error) {
	deadline, ok := ctx.Deadline()
	if !ok {
		return nil, sandbox.ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out, err := p.caller.Call(ctx, Request{Version: ProtocolVersion, Operation: "observe", Config: p.config, References: references, Deadline: deadline})
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	if err != nil || out.Version != ProtocolVersion || out.Info != nil || out.Command != nil || out.DeploymentValid || out.TemplateBuild != nil {
		return nil, runtimeobs.ErrUnavailable
	}
	switch out.ErrorCode {
	case "":
	case "invalid":
		return nil, sandbox.ErrInvalid
	case "ownership":
		return nil, sandbox.ErrOwnership
	default:
		// E2B API failures, including a rejected credential, leave rows unavailable.
		return nil, runtimeobs.ErrUnavailable
	}
	if len(out.Observations) != len(references) {
		return nil, sandbox.ErrInvalid
	}
	for index, observation := range out.Observations {
		if observation.Reference != references[index] {
			return nil, sandbox.ErrOwnership
		}
	}
	return out.Observations, nil
}

// sampleFromObservation maps E2B's latest point to the provider-neutral
// sample. E2B reports no cumulative CPU time, so usage seconds stay nil.
func sampleFromObservation(observation Observation, now time.Time) (runtimeobs.Sample, error) {
	switch observation.Status {
	case "observed":
	case "not_running":
		return runtimeobs.Sample{}, runtimeobs.ErrNotRunning
	case "unavailable":
		return runtimeobs.Sample{}, runtimeobs.ErrUnavailable
	case "ownership":
		return runtimeobs.Sample{}, sandbox.ErrOwnership
	default:
		// An unknown status breaks Core's own helper protocol.
		return runtimeobs.Sample{}, sandbox.ErrInvalid
	}
	// Malformed provider data leaves this row unavailable, not the whole page.
	if observation.ObservedAt == nil || observation.StartedAt == nil || observation.CPUCount == nil || observation.CPUUsedPct == nil ||
		observation.MemUsed == nil || observation.MemTotal == nil || !finite(*observation.CPUCount) || *observation.CPUCount <= 0 ||
		!finite(*observation.CPUUsedPct) || *observation.CPUUsedPct < 0 || *observation.MemTotal == 0 {
		return runtimeobs.Sample{}, runtimeobs.ErrUnavailable
	}
	// E2B timestamps use the provider's clock. Record a small lead at Core's
	// time so that a fresh point is not rejected as a future sample.
	observedAt, startedAt := *observation.ObservedAt, *observation.StartedAt
	if lead := observedAt.Sub(now); lead > 0 {
		if lead > maxClockLead {
			return runtimeobs.Sample{}, runtimeobs.ErrUnavailable
		}
		observedAt = now
	}
	if startedAt.After(observedAt) {
		return runtimeobs.Sample{}, runtimeobs.ErrUnavailable
	}
	ratio, capacity := *observation.CPUUsedPct/100, *observation.CPUCount
	memoryUsage, memoryLimit := *observation.MemUsed, *observation.MemTotal
	sample := runtimeobs.Sample{
		ObservedAt: observedAt, StartedAt: &startedAt,
		CPUUtilizationRatio: &ratio, CPUCapacityCores: &capacity,
		MemoryUsageBytes: &memoryUsage, MemoryLimitBytes: &memoryLimit,
	}
	// Templates with an envd older than E2B's disk metrics report no disk
	// capacity; keep disk unknown rather than an observed zero.
	if observation.DiskUsed != nil && observation.DiskTotal != nil && *observation.DiskTotal > 0 {
		diskUsage, diskLimit := *observation.DiskUsed, *observation.DiskTotal
		sample.DiskUsageBytes, sample.DiskLimitBytes = &diskUsage, &diskLimit
	}
	return sample, nil
}

func finite(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }
