package e2b

import (
	"errors"
	"testing"
	"time"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimeobs"
)

func TestObserveMapsMetricsAndKeepsUnmeasuredValuesNull(t *testing.T) {
	p, caller, running := fixture(t)
	now := time.Date(2026, 9, 25, 10, 31, 37, 0, time.UTC)
	p.now = func() time.Time { return now }
	started, observed := now.Add(-3*time.Second), now.Add(time.Second) // E2B's clock leads by one second.
	count, percent, memoryUsed, memoryTotal, diskUsed, diskTotal := 2.0, 19.55, uint64(183836672), uint64(2079141888), uint64(1593188352), uint64(23511863296)
	caller.response.Observation = &Observation{Status: "observed", ObservedAt: &observed, StartedAt: &started, CPUCount: &count, CPUUsedPct: &percent,
		MemUsed: &memoryUsed, MemTotal: &memoryTotal, DiskUsed: &diskUsed, DiskTotal: &diskTotal}
	target := runtimeobs.Target{TenantID: running.TenantID, EnvironmentID: running.EnvironmentID, Mode: v1.RuntimeModeManaged,
		Instance: runtimeobs.Instance{AllocationID: running.AllocationID, ProviderKey: p.config.InstallationID}}
	sample, err := p.Observe(bounded(t), target)
	if err != nil || len(caller.requests) != 1 || caller.requests[0].Operation != "observe" ||
		caller.requests[0].Reference != running || caller.requests[0].Deadline.IsZero() {
		t.Fatalf("observation was not one bounded helper request: err=%v %+v", err, caller.requests)
	}
	if !sample.ObservedAt.Equal(now) || !sample.StartedAt.Equal(started) ||
		*sample.CPUUtilizationRatio != .1955 || *sample.CPUCapacityCores != 2 || sample.CPUUsageSecondsTotal != nil ||
		*sample.MemoryUsageBytes != memoryUsed || *sample.MemoryLimitBytes != memoryTotal ||
		*sample.DiskUsageBytes != diskUsed || *sample.DiskLimitBytes != diskTotal {
		t.Fatalf("metrics were not mapped: %+v", sample)
	}

	caller.response.Observation.DiskTotal = nil
	if sample, err = p.Observe(bounded(t), target); err != nil || sample.DiskUsageBytes != nil || sample.DiskLimitBytes != nil {
		t.Fatalf("unreported disk was not null: %+v %v", sample, err)
	}
	caller.response.Observation.MemTotal = nil
	if _, err = p.Observe(bounded(t), target); !errors.Is(err, runtimeobs.ErrUnavailable) {
		t.Fatalf("malformed point = %v", err)
	}
	caller.response.Observation = &Observation{Status: "not_running"}
	if _, err = p.Observe(bounded(t), target); !errors.Is(err, runtimeobs.ErrNotRunning) {
		t.Fatalf("absent sandbox = %v", err)
	}
	caller.response.Observation = nil
	if _, err = p.Observe(bounded(t), target); err == nil {
		t.Fatal("a success without an observation was accepted")
	}
	caller.response.ErrorCode = "unconfirmed"
	if _, err = p.Observe(bounded(t), target); !errors.Is(err, runtimeobs.ErrUnavailable) {
		t.Fatalf("E2B failure was not unavailable: %v", err)
	}
}
