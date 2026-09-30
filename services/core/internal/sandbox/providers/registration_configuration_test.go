package providers

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/providercontract"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

// Only declaration reads are safe on this fixture. Any configuration or native
// call through the nil embedded interfaces fails the test immediately.
type registrationConfiguration struct {
	sandbox.ConfigurationAdapter
	sandbox.ConfigurationDiscoverer
	requirements sandbox.ConfigurationRequirements
}

func (a registrationConfiguration) Requirements() sandbox.ConfigurationRequirements {
	return a.requirements
}

// A declaration of Unsupported still requires an explicit rejection method.
type missingConfigurationDiscovery struct{ sandbox.ConfigurationAdapter }

func TestConfigurationRegistrationRejectsNilAndMissingDiscovery(t *testing.T) {
	registry := Builtin()
	a := registry.adapters["docker"]
	var typedNil *registrationConfiguration
	for _, configuration := range []sandbox.ConfigurationAdapter{
		nil, typedNil, missingConfigurationDiscovery{a.Configuration},
		missingConfigurationDiscovery{registry.adapters["e2b"].Configuration},
	} {
		a.Configuration = configuration
		if err := ValidateRegistration(a); !errors.Is(err, providercontract.ErrContract) {
			t.Fatalf("%T: %v", configuration, err)
		}
	}
}

func TestConfigurationRequirementsRejectEachOmission(t *testing.T) {
	registry := Builtin()
	a := registry.adapters["docker"]
	original := a.Configuration.Requirements()
	typ := reflect.TypeOf(original)
	for i := 0; i < typ.NumField(); i++ {
		t.Run(typ.Field(i).Name, func(t *testing.T) {
			requirements := original
			reflect.ValueOf(&requirements).Elem().Field(i).SetZero()
			a.Configuration = registrationConfiguration{requirements: requirements}
			if err := ValidateRegistration(a); !errors.Is(err, providercontract.ErrContract) {
				t.Fatal(err)
			}
		})
	}
}

func TestConfigurationRequirementsRejectInvalidDeclarations(t *testing.T) {
	registry := Builtin()
	for _, change := range []func(*sandbox.ConfigurationRequirements){
		func(r *sandbox.ConfigurationRequirements) { r.Credential = "automatic" },
		func(r *sandbox.ConfigurationRequirements) { r.PublicOrigin = "private" },
		func(r *sandbox.ConfigurationRequirements) { r.Discovery.State = "unknown" },
		func(r *sandbox.ConfigurationRequirements) { r.Discovery.Reason = "" },
		func(r *sandbox.ConfigurationRequirements) { r.Discovery.Reason = "https://private:key@host" },
		func(r *sandbox.ConfigurationRequirements) { r.Discovery.State = providercontract.Supported },
	} {
		a := registry.adapters["docker"]
		requirements := a.Configuration.Requirements()
		change(&requirements)
		a.Configuration = registrationConfiguration{requirements: requirements}
		if err := ValidateRegistration(a); !errors.Is(err, providercontract.ErrContract) {
			t.Fatal(err)
		}
	}
}

func TestConfigurationRequirementsDoNotInventDependencies(t *testing.T) {
	registry := Builtin()
	const kind = "configuration-requirement-test"
	// Required credentials are input policy. VerifyCredential is a separate
	// resource operation that may be Unsupported for this provider.
	for _, credential := range []sandbox.Requirement{sandbox.Required, sandbox.NotRequired} {
		for _, public := range []sandbox.Requirement{sandbox.Required, sandbox.NotRequired} {
			a := registry.adapters["docker"]
			requirements := a.Configuration.Requirements()
			requirements.Credential, requirements.PublicOrigin = credential, public
			a.Configuration = registrationConfiguration{requirements: requirements}
			if err := ValidateRegistration(a); err != nil {
				t.Fatal(err)
			}
			registry.adapters[kind] = a
			gotCredential, err := registry.UsesCredential(kind)
			if err != nil || gotCredential != (credential == sandbox.Required) {
				t.Fatalf("credential %s: value=%v error=%v", credential, gotCredential, err)
			}
			gotPublic, err := registry.RequiresPublicOrigin(kind)
			if err != nil || gotPublic != (public == sandbox.Required) {
				t.Fatalf("public origin %s: value=%v error=%v", public, gotPublic, err)
			}
		}
	}
}

type futureConfigurationDiscovery interface {
	sandbox.ConfigurationDiscoverer
	NextDiscovery(context.Context) error
}

func TestFutureConfigurationRequirementAndMethodNeedExplicitHandling(t *testing.T) {
	registry := Builtin()
	original := registry.adapters["docker"].Configuration.Requirements()
	fields := make([]reflect.StructField, 0, 4)
	typ := reflect.TypeOf(original)
	for i := 0; i < typ.NumField(); i++ {
		fields = append(fields, typ.Field(i))
	}
	fields = append(fields, reflect.StructField{Name: "FutureRequirement", Type: reflect.TypeFor[sandbox.Requirement]()})
	value := reflect.New(reflect.StructOf(fields)).Elem()
	for i := 0; i < typ.NumField(); i++ {
		value.Field(i).Set(reflect.ValueOf(original).Field(i))
	}
	value.Field(typ.NumField()).Set(reflect.ValueOf(sandbox.NotRequired))
	if err := validateConfigurationRequirements(value); !errors.Is(err, providercontract.ErrContract) {
		t.Fatal("new requirement silently inherited policy", err)
	}
	if err := validateConfigurationDiscoveryInterface(reflect.TypeFor[futureConfigurationDiscovery]()); !errors.Is(err, providercontract.ErrContract) {
		t.Fatal("new discovery method inherited support", err)
	}
}

func TestUnsupportedConfigurationDiscoveryMatchesAuthoredReason(t *testing.T) {
	registry := Builtin()
	for kind, a := range registry.adapters {
		support := a.Configuration.Requirements().Discovery
		if support.State != providercontract.Unsupported {
			continue
		}
		native := a.Configuration.(sandbox.ConfigurationDiscoverer)
		for _, read := range []func() ([]byte, error){
			func() ([]byte, error) {
				return native.DiscoverConfiguration(t.Context(), sandbox.ConfigurationDiscoveryInput{}, sandbox.ProcessPaths{})
			},
			func() ([]byte, error) {
				return registry.DiscoverConfiguration(t.Context(), kind, sandbox.ConfigurationDiscoveryInput{}, sandbox.ProcessPaths{})
			},
		} {
			result, err := read()
			reason, valid := providercontract.UnsupportedReason(err, "DiscoverConfiguration")
			if result != nil || !valid || reason != support.Reason {
				t.Fatalf("%s: result=%v reason=%s err=%v", kind, result, reason, err)
			}
		}
	}
}
