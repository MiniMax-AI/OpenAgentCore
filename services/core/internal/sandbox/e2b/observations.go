package e2b

import (
	"context"
	"math"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimeobs"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

// maxClockLead tolerates an E2B metrics timestamp slightly ahead of Core's
// clock. A larger lead is treated as an unavailable sample, never as fresh data.
const maxClockLead = 30 * time.Second

// Observation is the requested allocation's latest E2B metrics point. Status
// is observed, not_running, unavailable or ownership; only observed carries
// values. A malformed E2B point is unavailable. Values keep E2B's units:
// CPUUsedPct is a percentage of all CPUCount cores, and memory and disk are in
// bytes.
type Observation struct {
	Status                string
	ObservedAt, StartedAt *time.Time `json:",omitempty"`
	CPUCount, CPUUsedPct  *float64   `json:",omitempty"`
	MemUsed, MemTotal     *uint64    `json:",omitempty"`
	DiskUsed, DiskTotal   *uint64    `json:",omitempty"`
}

// Observe reads one allocation with one helper request. The helper takes the
// sandbox ID from its private receipt, confirms the running sandbox by its
// allocation labels and reads E2B's metrics. It never connects to, renews or
// changes a sandbox.
func (p *Provider) Observe(ctx context.Context, target runtimeobs.Target) (runtimeobs.Sample, error) {
	reference := sandbox.Reference{TenantID: target.TenantID, EnvironmentID: target.EnvironmentID, AllocationID: target.Instance.AllocationID}
	if target.Mode != runtimeobs.ModeManaged || !validReference(reference) {
		return runtimeobs.Sample{}, sandbox.ErrInvalid
	}
	if target.Instance.ProviderKey != p.config.InstallationID {
		return runtimeobs.Sample{}, sandbox.ErrOwnership
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		return runtimeobs.Sample{}, sandbox.ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return runtimeobs.Sample{}, err
	}
	out, err := p.caller.Call(ctx, Request{Version: ProtocolVersion, Operation: "observe", Config: p.config, Reference: reference, Deadline: deadline})
	if ctxErr := ctx.Err(); ctxErr != nil {
		return runtimeobs.Sample{}, ctxErr
	}
	if err != nil || out.Version != ProtocolVersion || out.Info != nil || out.Command != nil || out.DeploymentValid || out.TemplateBuild != nil {
		return runtimeobs.Sample{}, runtimeobs.ErrUnavailable
	}
	switch out.ErrorCode {
	case "":
	case "invalid":
		return runtimeobs.Sample{}, sandbox.ErrInvalid
	case "ownership":
		return runtimeobs.Sample{}, sandbox.ErrOwnership
	default:
		// E2B API failures, including a rejected credential, are unavailable.
		return runtimeobs.Sample{}, runtimeobs.ErrUnavailable
	}
	if out.Observation == nil {
		return runtimeobs.Sample{}, sandbox.ErrInvalid
	}
	return sampleFromObservation(*out.Observation, p.now())
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
	// Malformed provider data leaves this sample unavailable.
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
