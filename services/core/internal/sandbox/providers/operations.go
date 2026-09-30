package providers

import (
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/providercontract"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"reflect"
)

// ValidateBinding catches construction that disagrees with its registration.
func ValidateBinding(adapter Adapter, provider sandbox.SandboxProvider) error {
	if err := ValidateRegistration(adapter); err != nil {
		return err
	}
	if err := sandbox.ValidateProvider(provider); err != nil {
		return err
	}
	if adapter.Operations == nil || !reflect.DeepEqual(adapter.Operations(), provider.ProviderOperations()) {
		return providercontract.ErrContract
	}
	return nil
}
