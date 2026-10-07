package runtimeobs

import (
	"context"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/providercontract"
)

func observedOperations() providercontract.Operations {
	return providercontract.Operations{"Observe": {State: providercontract.Supported}}
}
func (*fixedSource) ProviderOperations() providercontract.Operations    { return observedOperations() }
func (blockingSource) ProviderOperations() providercontract.Operations  { return observedOperations() }
func (*countingSource) ProviderOperations() providercontract.Operations { return observedOperations() }

// sourceOf selects one fixed docker source for every page.
func sourceOf(source Source) func(context.Context) (Source, string, error) {
	return func(context.Context) (Source, string, error) { return source, "docker", nil }
}
