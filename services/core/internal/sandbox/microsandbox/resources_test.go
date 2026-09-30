package microsandbox

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimeobs"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

func observationTarget(t *testing.T, compute Compute) runtimeobs.Target {
	t.Helper()
	state, err := json.Marshal(struct {
		Current Compute `json:"current"`
	}{Current: compute})
	if err != nil {
		t.Fatal(err)
	}
	r := testRef()
	return runtimeobs.Target{
		TenantID: r.TenantID, SessionID: "55555555-5555-4555-8555-555555555555", EnvironmentID: r.EnvironmentID, Mode: runtimeobs.ModeManaged,
		Instance: runtimeobs.Instance{AllocationID: r.AllocationID, ProviderKey: testConfig().InstallationID, AllocationState: "running", ComputePhase: "running", ProviderState: state},
	}
}

func TestObserveUsesPersistedExactComputeAndNormalizesMetrics(t *testing.T) {
	config, reference := testConfig(), testRef()
	compute := Compute{Name: Name(config, reference, 3), ID: "local:current", Generation: 3, RestoredFrom: func() *SnapshotIdentity { value := testSnapshot(); return &value }()}
	// The snapshot fixture belongs to generation zero and is valid provenance for generation three.
	observed := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	provider, err := NewWithCaller(config, callerFunc(func(_ context.Context, request Request) (Response, error) {
		if request.Operation != "metrics" || request.Reference != reference || request.Compute.ID != compute.ID || request.Compute.Generation != compute.Generation {
			t.Fatalf("unexpected metrics request: %+v", request)
		}
		return Response{Version: ProtocolVersion, Metrics: &Metrics{
			ObservedAt: observed, Uptime: 5 * time.Minute, VCPUTimeNs: 2_500_000_000,
			MemoryBytes: 1024, MemoryLimitBytes: 2 * 1024 * 1024,
		}}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	sample, err := provider.Observe(deadline(t), observationTarget(t, compute))
	if err != nil {
		t.Fatal(err)
	}
	if sample.StartedAt == nil || !sample.StartedAt.Equal(observed.Add(-5*time.Minute)) || sample.CPUUsageSecondsTotal == nil || *sample.CPUUsageSecondsTotal != 2.5 || sample.CPUCapacityCores == nil || *sample.CPUCapacityCores != 2 || sample.MemoryUsageBytes == nil || *sample.MemoryUsageBytes != 1024 || sample.MemoryLimitBytes == nil || *sample.MemoryLimitBytes != 2*1024*1024 {
		t.Fatalf("bad normalized sample: %+v", sample)
	}
}

func TestObserveRejectsForeignOrMissingComputeBeforeHelper(t *testing.T) {
	config, reference := testConfig(), testRef()
	compute := Compute{Name: Name(config, reference, 0), ID: "local:current"}
	calls := 0
	provider, _ := NewWithCaller(config, callerFunc(func(context.Context, Request) (Response, error) { calls++; return Response{}, nil }))

	foreign := observationTarget(t, compute)
	foreign.Instance.ProviderKey = "66666666-6666-4666-8666-666666666666"
	if _, err := provider.Observe(deadline(t), foreign); !errors.Is(err, sandbox.ErrOwnership) {
		t.Fatalf("foreign installation accepted: %v", err)
	}
	missing := observationTarget(t, compute)
	missing.Instance.ProviderState = json.RawMessage(`{}`)
	if _, err := provider.Observe(deadline(t), missing); !errors.Is(err, sandbox.ErrOwnership) {
		t.Fatalf("missing compute accepted: %v", err)
	}
	if calls != 0 {
		t.Fatalf("invalid identity reached helper: %d calls", calls)
	}
}

func TestObserveWithoutExactReceiptAndSuspendedDoesNotWake(t *testing.T) {
	config := testConfig()
	calls := 0
	provider, _ := NewWithCaller(config, callerFunc(func(_ context.Context, request Request) (Response, error) {
		calls++
		return Response{}, nil
	}))
	target := observationTarget(t, Compute{Name: Name(config, testRef(), 0), ID: "unused"})
	target.Instance.ComputePhase, target.Instance.ProviderState = "disabled", nil
	if _, err := provider.Observe(deadline(t), target); !errors.Is(err, runtimeobs.ErrUnavailable) {
		t.Fatalf("receipt-less compute was sampled: %v", err)
	}
	target.Instance.ComputePhase = "suspended"
	if _, err := provider.Observe(deadline(t), target); !errors.Is(err, runtimeobs.ErrNotRunning) {
		t.Fatalf("suspended compute was not unavailable: %v", err)
	}
	if calls != 0 {
		t.Fatalf("unfenced compute reached helper: %d calls", calls)
	}
}

func TestObserveMapsStoppedAndMetricsUnavailable(t *testing.T) {
	config, reference := testConfig(), testRef()
	compute := Compute{Name: Name(config, reference, 0), ID: "local:current"}
	for _, test := range []struct {
		name     string
		response Response
		want     error
	}{
		{name: "stopped", response: Response{Version: ProtocolVersion, ErrorCode: "not_found"}, want: runtimeobs.ErrNotRunning},
		{name: "metrics unavailable", response: Response{Version: ProtocolVersion, ErrorCode: "metrics_unavailable"}, want: runtimeobs.ErrUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			provider, _ := NewWithCaller(config, callerFunc(func(context.Context, Request) (Response, error) { return test.response, nil }))
			if _, err := provider.Observe(deadline(t), observationTarget(t, compute)); !errors.Is(err, test.want) {
				t.Fatalf("error=%v want=%v", err, test.want)
			}
		})
	}
}

func TestSampleFromMetricsRejectsInvalidTimingAndLimit(t *testing.T) {
	config := testConfig()
	for _, metrics := range []Metrics{
		{},
		{ObservedAt: time.Unix(1, 0), Uptime: -time.Second, MemoryLimitBytes: 1},
		{ObservedAt: time.Unix(1, 0), Uptime: 2 * time.Second, MemoryLimitBytes: 1},
		{ObservedAt: time.Unix(1, 0), MemoryLimitBytes: 0},
	} {
		if _, err := sampleFromMetrics(config, metrics); err == nil {
			t.Fatalf("invalid metrics accepted: %+v", metrics)
		}
	}
}

func TestObserveKeepsIncarnationAcrossPollsAndCoreRestart(t *testing.T) {
	config, reference := testConfig(), testRef()
	compute := Compute{Name: Name(config, reference, 0), ID: "local:first"}
	startedAt := time.Date(2026, 9, 22, 12, 0, 0, 122000000, time.UTC)
	var previous runtimeobs.Sample
	for index, uptime := range []time.Duration{300001 * time.Millisecond, 301877 * time.Millisecond, time.Millisecond} {
		currentStart := startedAt
		cpu := uint64(2_500_000_000 + index*1_000_000_000)
		if index == 2 {
			compute = Compute{Name: Name(config, reference, 1), ID: "local:restored", Generation: 1, RestoredFrom: func() *SnapshotIdentity { s := testSnapshot(); return &s }()}
			currentStart = startedAt.Add(5 * time.Minute)
			cpu = 1_000_000
		}
		// Each poll uses a fresh Core provider, with no process-local time cache.
		provider, err := NewWithCaller(config, callerFunc(func(_ context.Context, request Request) (Response, error) {
			if request.Compute.ID != compute.ID || request.Compute.Generation != compute.Generation {
				t.Fatalf("wrong compute: %+v", request.Compute)
			}
			return Response{Version: ProtocolVersion, Metrics: &Metrics{ObservedAt: currentStart.Add(uptime), Uptime: uptime, VCPUTimeNs: cpu, MemoryLimitBytes: 8192}}, nil
		}))
		if err != nil {
			t.Fatal(err)
		}
		sample, err := provider.Observe(deadline(t), observationTarget(t, compute))
		if err != nil {
			t.Fatal(err)
		}
		if sample.StartedAt == nil || !sample.StartedAt.Equal(currentStart) {
			t.Fatalf("wrong start: %+v", sample)
		}
		if index == 1 {
			if !sample.StartedAt.Equal(*previous.StartedAt) || *sample.CPUUsageSecondsTotal-*previous.CPUUsageSecondsTotal != 1 {
				t.Fatal("repeated polls lost the CPU delta")
			}
		}
		if index == 2 && sample.StartedAt.Equal(*previous.StartedAt) {
			t.Fatal("restored compute reused the source incarnation")
		}
		previous = sample
	}
}
