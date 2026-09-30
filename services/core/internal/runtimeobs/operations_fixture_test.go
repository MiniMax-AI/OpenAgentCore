package runtimeobs

import (
	"context"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/providercontract"
)

func (*fixedSource) ProviderOperations() providercontract.Operations {
	return providercontract.Operations{"ResolveObservationSource": {State: providercontract.Supported}, "ObservationProviderType": {State: providercontract.Supported}, "Observe": {State: providercontract.Supported}, "ObserveBatch": {State: providercontract.Unsupported, Reason: "fixture_has_no_batch_observation"}}
}
func (*fixedSource) ObserveBatch(context.Context, []Target) ([]BatchResult, error) {
	return nil, &providercontract.UnsupportedError{Operation: "ObserveBatch", Reason: "fixture_has_no_batch_observation"}
}
func (blockingSource) ProviderOperations() providercontract.Operations {
	return providercontract.Operations{"ResolveObservationSource": {State: providercontract.Supported}, "ObservationProviderType": {State: providercontract.Supported}, "Observe": {State: providercontract.Supported}, "ObserveBatch": {State: providercontract.Unsupported, Reason: "fixture_has_no_batch_observation"}}
}
func (blockingSource) ObserveBatch(context.Context, []Target) ([]BatchResult, error) {
	return nil, &providercontract.UnsupportedError{Operation: "ObserveBatch", Reason: "fixture_has_no_batch_observation"}
}
func (*countingSource) ProviderOperations() providercontract.Operations {
	return providercontract.Operations{"ResolveObservationSource": {State: providercontract.Supported}, "ObservationProviderType": {State: providercontract.Supported}, "Observe": {State: providercontract.Supported}, "ObserveBatch": {State: providercontract.Unsupported, Reason: "fixture_has_no_batch_observation"}}
}
func (*countingSource) ObserveBatch(context.Context, []Target) ([]BatchResult, error) {
	return nil, &providercontract.UnsupportedError{Operation: "ObserveBatch", Reason: "fixture_has_no_batch_observation"}
}
func (*batchSource) ProviderOperations() providercontract.Operations {
	return providercontract.Operations{"ResolveObservationSource": {State: providercontract.Supported}, "ObservationProviderType": {State: providercontract.Supported}, "Observe": {State: providercontract.Supported}, "ObserveBatch": {State: providercontract.Supported}}
}

func (s *fixedSource) ResolveObservationSource(context.Context) (Source, error) { return s, nil }
func (*fixedSource) ObservationProviderType() string                            { return "fixture" }

func (s blockingSource) ResolveObservationSource(context.Context) (Source, error) { return s, nil }

func (s *countingSource) ResolveObservationSource(context.Context) (Source, error) { return s, nil }
func (*countingSource) ObservationProviderType() string                            { return "fixture" }

func (s *batchSource) ResolveObservationSource(context.Context) (Source, error) { return s, nil }
func (*batchSource) ObservationProviderType() string                            { return "fixture" }

func (s typedSource) ResolveObservationSource(context.Context) (Source, error) { return s, nil }

func (s *failingBatchSource) ResolveObservationSource(context.Context) (Source, error) { return s, nil }

func (s *unsupportedObservation) ResolveObservationSource(context.Context) (Source, error) {
	return s, nil
}
