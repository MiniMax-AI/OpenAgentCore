package runtimeobs

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/providercontract"
)

type unsupportedObservation struct{ *fixedSource }

func (*unsupportedObservation) ProviderOperations() providercontract.Operations {
	return providercontract.Operations{"Observe": {State: providercontract.Unsupported, Reason: "native_metrics_not_supported"}}
}
func TestUnsupportedObservationIsNotUnavailable(t *testing.T) {
	source := &unsupportedObservation{&fixedSource{}}
	target := Target{EnvironmentID: "environment", Mode: ModeManaged, Instance: Instance{AllocationID: "allocation", ProviderKey: "provider", AllocationState: "running"}}
	service, err := NewService(fixedResolver{target: target}, sourceOf(source))
	if err != nil {
		t.Fatal(err)
	}
	observation, err := service.ObserveSession(t.Context(), "tenant", "session")
	if err != nil || observation.Status != StatusUnsupported || observation.Reason != "native_metrics_not_supported" || source.calls != 0 {
		t.Fatal(observation, err, source.calls)
	}
}

// Exercise the existing service classifications, rather than a second list of codes.
func TestUnavailableReasonsMatchSharedFixture(t *testing.T) {
	raw, err := os.ReadFile("../providercontract/testdata/observation_reasons.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []struct {
			Reason      string
			Unavailable bool
		}
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	expected := map[string]bool{}
	for _, entry := range fixture.Cases {
		if entry.Unavailable {
			expected[entry.Reason] = true
		}
	}
	actual := map[string]bool{}
	for _, scenario := range []struct {
		state                  string
		resolveErr, observeErr error
	}{
		{state: "creating"}, {state: "cleanup_pending"}, {state: "released"},
		{resolveErr: ErrUnavailable},
		{state: "running", observeErr: ErrNotRunning},
		{state: "running", observeErr: context.DeadlineExceeded},
		{state: "running", observeErr: ErrUnavailable},
	} {
		target := Target{EnvironmentID: "environment", Mode: ModeManaged, Instance: Instance{AllocationID: "allocation", ProviderKey: "provider", AllocationState: scenario.state}}
		if scenario.resolveErr != nil {
			target.Instance = Instance{}
		}
		service, err := NewService(fixedResolver{target: target, err: scenario.resolveErr}, sourceOf(&fixedSource{err: scenario.observeErr}))
		if err != nil {
			t.Fatal(err)
		}
		observation, err := service.ObserveSession(t.Context(), "tenant", "session")
		if err != nil || observation.Status != StatusUnavailable {
			t.Fatalf("classification = %+v, %v", observation, err)
		}
		actual[observation.Reason] = true
	}
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("unavailable reasons = %v, fixture = %v", actual, expected)
	}
}
