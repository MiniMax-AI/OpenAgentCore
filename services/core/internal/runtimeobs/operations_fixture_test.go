package runtimeobs

import (
	"context"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/providercontract"
)

func (*fixedSource) ProviderOperations() providercontract.Operations {
	return providercontract.Operations{"Observe": {State: providercontract.Supported}, "ObserveBatch": {State: providercontract.Unsupported, Reason: "fixture_has_no_batch_observation"}}
}
func (*fixedSource) ObserveBatch(context.Context, []Target) ([]BatchResult, error) {
	return nil, &providercontract.UnsupportedError{Operation: "ObserveBatch", Reason: "fixture_has_no_batch_observation"}
}
func (blockingSource) ProviderOperations() providercontract.Operations {
	return providercontract.Operations{"Observe": {State: providercontract.Supported}, "ObserveBatch": {State: providercontract.Unsupported, Reason: "fixture_has_no_batch_observation"}}
}
func (blockingSource) ObserveBatch(context.Context, []Target) ([]BatchResult, error) {
	return nil, &providercontract.UnsupportedError{Operation: "ObserveBatch", Reason: "fixture_has_no_batch_observation"}
}
func (*countingSource) ProviderOperations() providercontract.Operations {
	return providercontract.Operations{"Observe": {State: providercontract.Supported}, "ObserveBatch": {State: providercontract.Unsupported, Reason: "fixture_has_no_batch_observation"}}
}
func (*countingSource) ObserveBatch(context.Context, []Target) ([]BatchResult, error) {
	return nil, &providercontract.UnsupportedError{Operation: "ObserveBatch", Reason: "fixture_has_no_batch_observation"}
}
func (*batchSource) ProviderOperations() providercontract.Operations {
	return providercontract.Operations{"Observe": {State: providercontract.Supported}, "ObserveBatch": {State: providercontract.Supported}}
}
