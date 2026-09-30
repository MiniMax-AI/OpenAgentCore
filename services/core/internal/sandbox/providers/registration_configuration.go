package providers

import (
	"errors"
	"fmt"
	"reflect"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/providercontract"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

func configurationRegistrationError() error {
	return fmt.Errorf("%w: invalid configuration registration", providercontract.ErrContract)
}

func validateConfigurationAdapter(configuration sandbox.ConfigurationAdapter) error {
	if configuration == nil {
		return configurationRegistrationError()
	}
	value := reflect.ValueOf(configuration)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		if value.IsNil() {
			return configurationRegistrationError()
		}
	}
	discovery := reflect.TypeFor[sandbox.ConfigurationDiscoverer]()
	if !value.Type().Implements(discovery) {
		return configurationRegistrationError()
	}
	if err := validateConfigurationDiscoveryInterface(discovery); err != nil {
		return err
	}
	return validateConfigurationRequirements(reflect.ValueOf(configuration.Requirements()))
}

// Every discovery method needs its own authored requirement. A future method
// cannot inherit the existing Discovery decision merely by being implemented.
func validateConfigurationDiscoveryInterface(discovery reflect.Type) error {
	if discovery.NumMethod() != 1 {
		return configurationRegistrationError()
	}
	if _, exists := discovery.MethodByName("DiscoverConfiguration"); !exists {
		return configurationRegistrationError()
	}
	return nil
}

// Check field names as well as values so new requirements cannot bypass the
// gate. This owns only configuration requirements, not resource operations.
func validateConfigurationRequirements(value reflect.Value) error {
	if value.Kind() != reflect.Struct || value.NumField() != 3 {
		return configurationRegistrationError()
	}
	for i := 0; i < value.NumField(); i++ {
		switch value.Type().Field(i).Name {
		case "Credential", "PublicOrigin":
			requirement, ok := value.Field(i).Interface().(sandbox.Requirement)
			if !ok || (requirement != sandbox.Required && requirement != sandbox.NotRequired) {
				return configurationRegistrationError()
			}
		case "Discovery":
			support, ok := value.Field(i).Interface().(providercontract.Support)
			if !ok {
				return configurationRegistrationError()
			}
			if err := support.Check("DiscoverConfiguration"); err != nil && !errors.Is(err, providercontract.ErrUnsupported) {
				return configurationRegistrationError()
			}
		default:
			return configurationRegistrationError()
		}
	}
	return nil
}
