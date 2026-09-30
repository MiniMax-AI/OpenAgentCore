package runtimeobs

import (
	"context"
	"errors"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/providercontract"
	"testing"
	"time"
)

type failingBatchSource struct {
	*fixedSource
	batchErr error
	batches  int
}

func (*failingBatchSource) ProviderOperations() providercontract.Operations {
	return providercontract.Operations{"Observe": {State: providercontract.Supported}, "ObserveBatch": {State: providercontract.Supported}}
}
func (s *failingBatchSource) ObserveBatch(context.Context, []Target) ([]BatchResult, error) {
	s.batches++
	return nil, s.batchErr
}
func TestBatchFallbackRequiresExplicitSafeUnsupported(t *testing.T) {
	for _, test := range []struct {
		name     string
		err      error
		fallback bool
	}{
		{"unsupported", &providercontract.UnsupportedError{Operation: "ObserveBatch", Reason: "native_batch_not_supported"}, true},
		{"unavailable", ErrUnavailable, false},
		{"timeout", context.DeadlineExceeded, false},
		{"failure", errors.New("provider failed"), false},
		{"bare unsupported", providercontract.ErrUnsupported, false},
		{"unsafe unsupported", &providercontract.UnsupportedError{Operation: "ObserveBatch", Reason: "private endpoint / key"}, false},
		{"wrong operation", &providercontract.UnsupportedError{Operation: "Observe", Reason: "not_supported"}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			now := time.Now()
			ratio := 0.5
			source := &failingBatchSource{fixedSource: &fixedSource{sample: Sample{ObservedAt: now, CPUUtilizationRatio: &ratio}}, batchErr: test.err}
			target := Target{EnvironmentID: "environment", Mode: ModeManaged, Instance: Instance{AllocationID: "allocation", ProviderKey: "provider", AllocationState: "running"}}
			service, err := NewService(fixedResolver{target: target}, map[string]Source{"provider": source})
			if err != nil {
				t.Fatal(err)
			}
			observations, errs := service.ObserveSessions(t.Context(), []SessionIdentity{{TenantID: "tenant", SessionID: "session"}}, PageOptions{})
			if source.batches != 1 || (source.calls == 1) != test.fallback {
				t.Fatalf("batch=%d single=%d", source.batches, source.calls)
			}
			if test.fallback && (errs[0] != nil || observations[0].Status != StatusObserved) {
				t.Fatal(observations, errs)
			}
		})
	}
}

type unsupportedObservation struct{ *fixedSource }

func (*unsupportedObservation) ProviderOperations() providercontract.Operations {
	return providercontract.Operations{"Observe": {State: providercontract.Unsupported, Reason: "native_metrics_not_supported"}, "ObserveBatch": {State: providercontract.Unsupported, Reason: "native_metrics_not_supported"}}
}
func TestUnsupportedObservationIsNotUnavailable(t *testing.T) {
	source := &unsupportedObservation{&fixedSource{}}
	target := Target{EnvironmentID: "environment", Mode: ModeManaged, Instance: Instance{AllocationID: "allocation", ProviderKey: "provider", AllocationState: "running"}}
	service, err := NewService(fixedResolver{target: target}, map[string]Source{"provider": source})
	if err != nil {
		t.Fatal(err)
	}
	observation, err := service.ObserveSession(t.Context(), "tenant", "session")
	if err != nil || observation.Status != StatusUnsupported || observation.Reason != "native_metrics_not_supported" || source.calls != 0 {
		t.Fatal(observation, err, source.calls)
	}
}
