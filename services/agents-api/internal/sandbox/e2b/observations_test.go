package e2b

import (
	"errors"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/runtimeobs"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
	"github.com/google/uuid"
)

func TestObserveBatchMapsMetricsAndKeepsUnmeasuredValuesNull(t *testing.T) {
	p, caller, running := fixture(t)
	stopped := sandbox.Reference{TenantID: uuid.NewString(), EnvironmentID: uuid.NewString(), AllocationID: uuid.NewString()}
	now := time.Date(2026, 9, 25, 10, 31, 37, 0, time.UTC)
	p.now = func() time.Time { return now }
	started, observed := now.Add(-3*time.Second), now.Add(time.Second) // E2B's clock leads by one second.
	count, percent, memoryUsed, memoryTotal, diskUsed, diskTotal := 2.0, 19.55, uint64(183836672), uint64(2079141888), uint64(1593188352), uint64(23511863296)
	caller.response.Observations = []Observation{
		{Reference: running, Status: "observed", ObservedAt: &observed, StartedAt: &started, CPUCount: &count, CPUUsedPct: &percent,
			MemUsed: &memoryUsed, MemTotal: &memoryTotal, DiskUsed: &diskUsed, DiskTotal: &diskTotal},
		{Reference: stopped, Status: "not_running"},
	}
	targets := []runtimeobs.Target{}
	for _, reference := range []sandbox.Reference{running, stopped} {
		targets = append(targets, runtimeobs.Target{TenantID: reference.TenantID, EnvironmentID: reference.EnvironmentID, Mode: runtimeobs.ModeManaged,
			Instance: runtimeobs.Instance{AllocationID: reference.AllocationID, ProviderKey: p.config.InstallationID}})
	}
	results, ok := p.ObserveBatch(bounded(t), targets)
	if !ok || len(caller.requests) != 1 || caller.requests[0].Operation != "observe" || len(caller.requests[0].References) != 2 ||
		caller.requests[0].References[1] != stopped || caller.requests[0].Deadline.IsZero() {
		t.Fatalf("batch was not one bounded helper request: ok=%v %+v", ok, caller.requests)
	}
	sample := results[0].Sample
	if results[0].Err != nil || !sample.ObservedAt.Equal(now) || !sample.StartedAt.Equal(started) ||
		*sample.CPUUtilizationRatio != .1955 || *sample.CPUCapacityCores != 2 || sample.CPUUsageSecondsTotal != nil ||
		*sample.MemoryUsageBytes != memoryUsed || *sample.MemoryLimitBytes != memoryTotal ||
		*sample.DiskUsageBytes != diskUsed || *sample.DiskLimitBytes != diskTotal {
		t.Fatalf("metrics were not mapped: %+v %v", sample, results[0].Err)
	}
	if !errors.Is(results[1].Err, runtimeobs.ErrNotRunning) {
		t.Fatalf("absent sandbox = %v", results[1].Err)
	}

	caller.response.Observations[0].DiskTotal = nil
	results, _ = p.ObserveBatch(bounded(t), targets)
	if results[0].Err != nil || results[0].Sample.DiskUsageBytes != nil || results[0].Sample.DiskLimitBytes != nil {
		t.Fatalf("unreported disk was not null: %+v %v", results[0].Sample, results[0].Err)
	}
	caller.response.Observations[0].MemTotal = nil
	results, _ = p.ObserveBatch(bounded(t), targets)
	if !errors.Is(results[0].Err, runtimeobs.ErrUnavailable) || !errors.Is(results[1].Err, runtimeobs.ErrNotRunning) {
		t.Fatalf("a malformed point affected more than its row: %+v", results)
	}
	caller.response.ErrorCode = "unconfirmed"
	results, _ = p.ObserveBatch(bounded(t), targets)
	if !errors.Is(results[0].Err, runtimeobs.ErrUnavailable) || !errors.Is(results[1].Err, runtimeobs.ErrUnavailable) {
		t.Fatalf("E2B failure was not unavailable: %+v", results)
	}
}
