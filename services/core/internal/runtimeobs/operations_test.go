package runtimeobs

import (
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
