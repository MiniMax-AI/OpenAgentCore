package runtimeobs

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"
)

type fixedResolver struct {
	target Target
	err    error
}

func (r fixedResolver) Resolve(_ context.Context, tenant, session string) (Target, error) {
	target := r.target
	if target.TenantID == "" {
		target.TenantID = tenant
	}
	if target.SessionID == "" {
		target.SessionID = session
	}
	return target, r.err
}

type fixedSource struct {
	sample Sample
	err    error
	calls  int
}

func (s *fixedSource) Observe(context.Context, Target) (Sample, error) {
	s.calls++
	return s.sample, s.err
}

type typedSource struct {
	*fixedSource
	providerType string
}

func (s typedSource) ObservationProviderType() string { return s.providerType }

type blockingSource struct{}

func (blockingSource) Observe(ctx context.Context, _ Target) (Sample, error) {
	<-ctx.Done()
	return Sample{}, ctx.Err()
}

func (blockingSource) ObservationProviderType() string { return "docker" }

func TestServiceDoesNotCallSourcesForUnsupportedModes(t *testing.T) {
	for _, mode := range []Mode{ModeNone, ModeSelfHosted} {
		source := &fixedSource{}
		target := Target{Mode: mode}
		if mode == ModeSelfHosted {
			target.EnvironmentID = "environment"
		}
		service, err := NewService(fixedResolver{target: target}, map[string]Source{"provider": source})
		if err != nil {
			t.Fatal(err)
		}
		observation, err := service.ObserveSession(t.Context(), "tenant", "session")
		if err != nil || observation.Status != StatusUnsupported || observation.Reason != "runtime_mode_not_observable" || source.calls != 0 {
			t.Fatalf("unsupported mode touched a source: %+v %v calls=%d", observation, err, source.calls)
		}
	}
}

func TestServicePreservesUnavailableAndObservedZero(t *testing.T) {
	target := Target{EnvironmentID: "environment", Mode: ModeManaged, Instance: Instance{AllocationID: "allocation", ProviderKey: "provider", AllocationState: "running"}}
	service, err := NewService(fixedResolver{target: target}, nil)
	if err != nil {
		t.Fatal(err)
	}
	observation, err := service.ObserveSession(t.Context(), "tenant", "session")
	if err != nil || observation.Status != StatusUnavailable || observation.Reason != "source_not_configured" || observation.Sample != nil {
		t.Fatalf("missing source was not unavailable: %+v %v", observation, err)
	}

	zeroCPU := float64(0)
	zeroMemory := uint64(0)
	now := time.Date(2026, 9, 22, 1, 0, 0, 0, time.UTC)
	source := &fixedSource{sample: Sample{ObservedAt: now, CPUUsageSecondsTotal: &zeroCPU, MemoryUsageBytes: &zeroMemory}}
	service, err = NewService(fixedResolver{target: target}, map[string]Source{"provider": source})
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return now }
	observation, err = service.ObserveSession(t.Context(), "tenant", "session")
	if err != nil || observation.Status != StatusObserved || observation.Sample == nil || observation.Sample.CPUUsageSecondsTotal == nil || observation.Sample.MemoryUsageBytes == nil {
		t.Fatalf("observed zero was lost: %+v %v", observation, err)
	}
}

func TestServiceMapsOnlyDeclaredUnavailability(t *testing.T) {
	target := Target{EnvironmentID: "environment", Mode: ModeManaged, Instance: Instance{AllocationID: "allocation", ProviderKey: "provider", AllocationState: "running"}}
	for _, tc := range []struct {
		err        error
		wantReason string
		wantError  bool
	}{
		{err: ErrUnavailable, wantReason: "sample_unavailable"},
		{err: ErrNotRunning, wantReason: "runtime_not_running"},
		{err: context.DeadlineExceeded, wantReason: "sample_timeout"},
		{err: errors.New("Docker permission denied"), wantError: true},
	} {
		service, err := NewService(fixedResolver{target: target}, map[string]Source{
			"provider": typedSource{fixedSource: &fixedSource{err: tc.err}, providerType: "docker"},
		})
		if err != nil {
			t.Fatal(err)
		}
		observation, err := service.ObserveSession(t.Context(), "tenant", "session")
		if (err != nil) != tc.wantError {
			t.Fatalf("wrong error classification: %+v %v", observation, err)
		}
		if !tc.wantError && (observation.Status != StatusUnavailable || observation.Reason != tc.wantReason || observation.ProviderType != "docker") {
			t.Fatalf("declared unavailability was not mapped: %+v", observation)
		}
	}
}

func TestServiceMapsAnActualSourceDeadlineWithoutLeakingIt(t *testing.T) {
	target := Target{
		EnvironmentID: "environment", Mode: ModeManaged,
		Instance: Instance{AllocationID: "allocation", ProviderKey: "provider", AllocationState: "running"},
	}
	service, err := NewService(fixedResolver{target: target}, map[string]Source{"provider": blockingSource{}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()
	observation, err := service.ObserveSession(ctx, "tenant", "session")
	if err != nil || observation.Status != StatusUnavailable || observation.Reason != "sample_timeout" || observation.ProviderType != "docker" {
		t.Fatalf("source deadline was not safely classified: %+v %v", observation, err)
	}
}

func TestServiceClassifiesResolverAndTerminalAllocationUnavailability(t *testing.T) {
	now := time.Date(2026, 9, 22, 1, 0, 0, 0, time.UTC)
	service, err := NewService(fixedResolver{target: Target{SessionID: "session", EnvironmentID: "environment", Mode: ModeManaged}, err: ErrUnavailable}, nil)
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return now }
	observation, err := service.ObserveSession(t.Context(), "tenant", "session")
	if err != nil || observation.Status != StatusUnavailable || observation.Reason != "allocation_pending" || !observation.ResolvedAt.Equal(now) {
		t.Fatalf("pending allocation was not classified: %+v %v", observation, err)
	}

	source := &fixedSource{}
	target := Target{EnvironmentID: "environment", Mode: ModeManaged, Instance: Instance{AllocationID: "allocation", ProviderKey: "provider", AllocationState: "creating"}}
	service, err = NewService(fixedResolver{target: target}, map[string]Source{"provider": source})
	if err != nil {
		t.Fatal(err)
	}
	observation, err = service.ObserveSession(t.Context(), "tenant", "session")
	if err != nil || observation.Status != StatusUnavailable || observation.Reason != "allocation_pending" || source.calls != 0 {
		t.Fatalf("creating allocation reached its provider: %+v %v calls=%d", observation, err, source.calls)
	}

	target = Target{EnvironmentID: "environment", Mode: ModeManaged, Instance: Instance{AllocationID: "allocation", ProviderKey: "provider", AllocationState: "released"}}
	service, err = NewService(fixedResolver{target: target}, map[string]Source{"provider": &fixedSource{}})
	if err != nil {
		t.Fatal(err)
	}
	observation, err = service.ObserveSession(t.Context(), "tenant", "session")
	if err != nil || observation.Status != StatusUnavailable || observation.Reason != "runtime_not_running" {
		t.Fatalf("released allocation was not classified: %+v %v", observation, err)
	}

	source = &fixedSource{}
	target = Target{EnvironmentID: "environment", Mode: ModeManaged, Instance: Instance{
		AllocationID: "allocation", ProviderKey: "provider", AllocationState: "running", AllocationCreatedAt: time.Now().Add(time.Hour),
	}}
	service, err = NewService(fixedResolver{target: target}, map[string]Source{"provider": source})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ObserveSession(t.Context(), "tenant", "session"); err == nil || source.calls != 0 {
		t.Fatalf("future allocation creation reached its provider: %v calls=%d", err, source.calls)
	}
}

func TestServiceRejectsUnsafeProviderSamples(t *testing.T) {
	target := Target{EnvironmentID: "environment", Mode: ModeManaged, Instance: Instance{AllocationID: "allocation", ProviderKey: "provider", AllocationState: "running"}}
	now := time.Date(2026, 9, 22, 1, 0, 0, 0, time.UTC)
	preEpoch := time.Unix(-1, 0).UTC()
	tooLarge := uint64(1 << 53)
	for _, sample := range []Sample{
		{ObservedAt: preEpoch},
		{ObservedAt: now, StartedAt: &preEpoch},
		{ObservedAt: now, CPUUsageSecondsTotal: float64Pointer(-1)},
		{ObservedAt: now, CPUUsageSecondsTotal: float64Pointer(math.NaN())},
		{ObservedAt: now, CPUUsageSecondsTotal: float64Pointer(math.Inf(1))},
		{ObservedAt: now, CPUCapacityCores: float64Pointer(0)},
		{ObservedAt: now, MemoryUsageBytes: &tooLarge},
		{ObservedAt: now, MemoryLimitBytes: &tooLarge},
	} {
		service, err := NewService(fixedResolver{target: target}, map[string]Source{"provider": &fixedSource{sample: sample}})
		if err != nil {
			t.Fatal(err)
		}
		service.now = func() time.Time { return now }
		if _, err := service.ObserveSession(t.Context(), "tenant", "session"); err == nil {
			t.Fatalf("unsafe sample accepted: %+v", sample)
		}
	}
}

func TestServiceRejectsMismatchedResolvedOwnership(t *testing.T) {
	for _, target := range []Target{
		{TenantID: "other", SessionID: "session", Mode: ModeNone},
		{TenantID: "tenant", SessionID: "other", Mode: ModeNone},
		{TenantID: "tenant", SessionID: "session", EnvironmentID: "unexpected", Mode: ModeNone},
		{TenantID: "tenant", SessionID: "session", Mode: ModeSelfHosted},
	} {
		service, err := NewService(fixedResolver{target: target}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := service.ObserveSession(t.Context(), "tenant", "session"); err == nil {
			t.Fatalf("mismatched ownership accepted: %+v", target)
		}
	}
}

func float64Pointer(value float64) *float64 { return &value }
