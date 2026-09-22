package runtimeobs

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fixedResolver struct {
	target Target
	err    error
}

func (r fixedResolver) Resolve(context.Context, string, string) (Target, error) {
	return r.target, r.err
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

func TestServiceDoesNotCallSourcesForUnsupportedModes(t *testing.T) {
	for _, mode := range []Mode{ModeNone, ModeSelfHosted} {
		source := &fixedSource{}
		service, err := NewService(fixedResolver{target: Target{Mode: mode}}, map[string]Source{"provider": source})
		if err != nil {
			t.Fatal(err)
		}
		observation, err := service.ObserveSession(t.Context(), "tenant", "session")
		if err != nil || observation.Status != StatusUnsupported || source.calls != 0 {
			t.Fatalf("unsupported mode touched a source: %+v %v calls=%d", observation, err, source.calls)
		}
	}
}

func TestServicePreservesUnavailableAndObservedZero(t *testing.T) {
	target := Target{Mode: ModeManaged, Instance: Instance{AllocationID: "allocation", ProviderKey: "provider"}}
	service, err := NewService(fixedResolver{target: target}, nil)
	if err != nil {
		t.Fatal(err)
	}
	observation, err := service.ObserveSession(t.Context(), "tenant", "session")
	if err != nil || observation.Status != StatusUnavailable || observation.Sample != nil {
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
	target := Target{Mode: ModeManaged, Instance: Instance{AllocationID: "allocation", ProviderKey: "provider"}}
	for _, tc := range []struct {
		err       error
		wantError bool
	}{
		{err: ErrUnavailable},
		{err: errors.New("Docker permission denied"), wantError: true},
	} {
		service, err := NewService(fixedResolver{target: target}, map[string]Source{"provider": &fixedSource{err: tc.err}})
		if err != nil {
			t.Fatal(err)
		}
		observation, err := service.ObserveSession(t.Context(), "tenant", "session")
		if (err != nil) != tc.wantError {
			t.Fatalf("wrong error classification: %+v %v", observation, err)
		}
		if !tc.wantError && observation.Status != StatusUnavailable {
			t.Fatalf("declared unavailability was not mapped: %+v", observation)
		}
	}
}
