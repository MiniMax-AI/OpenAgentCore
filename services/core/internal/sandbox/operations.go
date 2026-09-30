package sandbox

import (
	"errors"
	"fmt"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/providercontract"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimeobs"
	"reflect"
)

// These existing interfaces are the canonical operation inventory. Declarations
// must cover every method, including explicit unsupported implementations.
var providerInterfaces = []reflect.Type{
	reflect.TypeFor[SandboxProvider](), reflect.TypeFor[SuspensionProvider](),
	reflect.TypeFor[SelectionDiscoverer](), reflect.TypeFor[CredentialVerifier](),
	reflect.TypeFor[runtimeobs.SourceResolver](), reflect.TypeFor[runtimeobs.Source](), reflect.TypeFor[runtimeobs.BatchSource](),
}

func ValidateProvider(p SandboxProvider) error {
	if err := providercontract.Validate(p, providerInterfaces...); err != nil {
		return err
	}
	if err := runtimeobs.ValidateSource(p.(runtimeobs.Source)); err != nil {
		return err
	}
	return ValidateOperations(p.ProviderOperations())
}

// ValidateOperations rejects omitted, unknown and contradictory declarations.
func ValidateOperations(operations providercontract.Operations) error {
	known := map[string]bool{}
	for _, contract := range providerInterfaces {
		for i := 0; i < contract.NumMethod(); i++ {
			name := contract.Method(i).Name
			if name != "ProviderOperations" {
				known[name] = true
				if err := operations[name].Check(name); err != nil && !errors.Is(err, providercontract.ErrUnsupported) {
					return err
				}
			}
		}
	}
	for name := range operations {
		if !known[name] {
			return fmt.Errorf("%w: unknown operation %s", providercontract.ErrContract, name)
		}
	}
	required := reflect.TypeFor[SandboxProvider]()
	for i := 0; i < required.NumMethod(); i++ {
		name := required.Method(i).Name
		if name != "ProviderOperations" && operations[name].State != providercontract.Supported {
			return fmt.Errorf("%w: required operation %s", providercontract.ErrContract, name)
		}
	}
	for _, name := range []string{"ObservationProviderType", "ResolveObservationSource"} {
		if operations[name].State != providercontract.Supported {
			return fmt.Errorf("%w: required operation %s", providercontract.ErrContract, name)
		}
	}
	// The checkpoint lifecycle is indivisible: partial cleanup or restore support
	// cannot safely own a compute incarnation.
	checkpoint := reflect.TypeFor[SuspensionProvider]()
	for i := 0; i < checkpoint.NumMethod(); i++ {
		name := checkpoint.Method(i).Name
		if _, required := reflect.TypeFor[SandboxProvider]().MethodByName(name); !required && operations[name].State != operations["Initial"].State {
			return fmt.Errorf("%w: incomplete checkpoint lifecycle", providercontract.ErrContract)
		}
	}
	if operations["ObserveBatch"].State == providercontract.Supported && operations["Observe"].State != providercontract.Supported {
		return fmt.Errorf("%w: batch observation requires observation", providercontract.ErrContract)
	}
	return nil
}

func SupportsSuspension(p SandboxProvider) bool {
	return providercontract.Require(p, "Initial") == nil
}

func Suspension(p SandboxProvider) (SuspensionProvider, error) {
	if err := providercontract.Require(p, "Initial"); err != nil {
		return nil, err
	}
	cp, ok := p.(SuspensionProvider)
	if !ok {
		return nil, providercontract.ErrContract
	}
	return cp, nil
}
