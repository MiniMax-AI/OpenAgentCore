package opensandbox

import (
	"context"
	"errors"
	"math"
	"time"

	osdk "github.com/alibaba/OpenSandbox/sdks/sandbox/go"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimeobs"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

const bytesPerMiB = 1 << 20

// Observe reads execd's metrics for one running, owned sandbox. It never
// writes, renews or restarts anything.
func (p *Provider) Observe(ctx context.Context, target runtimeobs.Target) (runtimeobs.Sample, error) {
	r := sandbox.Reference{TenantID: target.TenantID, EnvironmentID: target.EnvironmentID, AllocationID: target.Instance.AllocationID}
	if target.Mode != runtimeobs.ModeManaged || !validReference(r) {
		return runtimeobs.Sample{}, sandbox.ErrInvalid
	}
	if target.Instance.ProviderKey != p.installation {
		return runtimeobs.Sample{}, sandbox.ErrOwnership
	}
	if _, ok := ctx.Deadline(); !ok {
		return runtimeobs.Sample{}, sandbox.ErrInvalid
	}
	got, item, err := p.lookup(ctx, r)
	switch {
	case errors.Is(err, sandbox.ErrNotFound):
		return runtimeobs.Sample{}, runtimeobs.ErrNotRunning
	case errors.Is(err, sandbox.ErrOwnership):
		return runtimeobs.Sample{}, err
	case err != nil:
		return runtimeobs.Sample{}, runtimeobs.ErrUnavailable
	case got.State != "running":
		return runtimeobs.Sample{}, runtimeobs.ErrNotRunning
	}
	live, err := osdk.ConnectSandbox(ctx, p.connection, item.ID)
	if err != nil {
		return runtimeobs.Sample{}, runtimeobs.ErrUnavailable
	}
	metrics, err := live.GetMetrics(ctx)
	if err != nil {
		return runtimeobs.Sample{}, runtimeobs.ErrUnavailable
	}
	return sample(*metrics, item.CreatedAt, p.now())
}

// sample maps execd's metrics. Malformed provider data leaves the sample
// unavailable; execd reports no cumulative CPU time, so usage seconds stay nil.
func sample(m osdk.Metrics, startedAt, now time.Time) (runtimeobs.Sample, error) {
	if m.Timestamp <= 0 || !finite(m.CPUCount) || m.CPUCount <= 0 || !finite(m.CPUUsedPct) || m.CPUUsedPct < 0 ||
		!finite(m.MemUsedMB) || m.MemUsedMB < 0 || !finite(m.MemTotalMB) || m.MemTotalMB <= 0 {
		return runtimeobs.Sample{}, runtimeobs.ErrUnavailable
	}
	// execd's clock is the provider's: record a small lead at Core's time so a
	// fresh point is not rejected as a future sample.
	observedAt := time.UnixMilli(m.Timestamp)
	if lead := observedAt.Sub(now); lead > 0 {
		if lead > maxClockLead {
			return runtimeobs.Sample{}, runtimeobs.ErrUnavailable
		}
		observedAt = now
	}
	if startedAt.IsZero() || startedAt.After(observedAt) {
		return runtimeobs.Sample{}, runtimeobs.ErrUnavailable
	}
	ratio, capacity := m.CPUUsedPct/100, m.CPUCount
	used, total := uint64(m.MemUsedMB*bytesPerMiB), uint64(m.MemTotalMB*bytesPerMiB)
	return runtimeobs.Sample{
		ObservedAt: observedAt, StartedAt: &startedAt,
		CPUUtilizationRatio: &ratio, CPUCapacityCores: &capacity,
		MemoryUsageBytes: &used, MemoryLimitBytes: &total,
	}, nil
}

func finite(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }
