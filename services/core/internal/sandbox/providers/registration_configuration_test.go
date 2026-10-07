package providers

import (
	"context"
	"encoding/json"
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
	requirements sandbox.ConfigurationRequirements
}

func (a registrationConfiguration) Requirements() sandbox.ConfigurationRequirements {
	return a.requirements
}

func TestConfigurationRegistrationRejectsNil(t *testing.T) {
	registry := Builtin()
	a := registry.adapters["docker"]
	var typedNil *registrationConfiguration
	for _, configuration := range []sandbox.ConfigurationAdapter{nil, typedNil} {
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

func TestFutureConfigurationRequirementNeedsExplicitHandling(t *testing.T) {
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
}

func TestUnsupportedSetupOperationsMatchAuthoredReasons(t *testing.T) {
	registry := Builtin()
	for kind, a := range registry.adapters {
		requirements := a.Configuration.Requirements()
		direct := sandbox.DirectConfig{Selection: sandbox.Selection{Provider: kind}}
		discover := func(read func() (json.RawMessage, error)) func() error {
			return func() error {
				if result, err := read(); result != nil {
					return errors.New("fabricated catalog")
				} else {
					return err
				}
			}
		}
		for _, operation := range []struct {
			name    string
			support providercontract.Support
			calls   []func() error
		}{
			{"DiscoverConfiguration", requirements.Discovery, []func() error{
				discover(func() (json.RawMessage, error) {
					return a.Configuration.DiscoverConfiguration(t.Context(), sandbox.ConfigurationDiscoveryInput{}, sandbox.ProcessPaths{})
				}),
				discover(func() (json.RawMessage, error) {
					return registry.DiscoverConfiguration(t.Context(), kind, sandbox.ConfigurationDiscoveryInput{}, sandbox.ProcessPaths{})
				}),
			}},
			{"DiscoverSelection", requirements.SelectionDiscovery, []func() error{
				func() error { _, err := a.Configuration.DiscoverSelection(t.Context(), direct); return err },
				func() error { _, err := registry.DiscoverSelection(t.Context(), direct); return err },
			}},
			{"VerifyCredential", requirements.CredentialVerification, []func() error{
				func() error { return a.Configuration.VerifyCredential(t.Context(), direct, nil) },
				func() error { return registry.VerifyCredential(t.Context(), direct, nil) },
			}},
		} {
			if operation.support.State != providercontract.Unsupported {
				continue
			}
			for _, call := range operation.calls {
				err := call()
				if reason, valid := providercontract.UnsupportedReason(err, operation.name); !valid || reason != operation.support.Reason {
					t.Fatalf("%s %s: reason=%s err=%v", kind, operation.name, reason, err)
				}
			}
		}
	}
}

// contradictingConfiguration declares every setup operation Supported and then
// reports each as Unsupported.
type contradictingConfiguration struct{ registrationConfiguration }

func (contradictingConfiguration) DiscoverConfiguration(context.Context, sandbox.ConfigurationDiscoveryInput, sandbox.ProcessPaths) (json.RawMessage, error) {
	return nil, &providercontract.UnsupportedError{Operation: "DiscoverConfiguration", Reason: "not_ready"}
}
func (contradictingConfiguration) DiscoverSelection(context.Context, sandbox.DirectConfig) (sandbox.Selection, error) {
	return sandbox.Selection{}, &providercontract.UnsupportedError{Operation: "DiscoverSelection", Reason: "not_ready"}
}
func (contradictingConfiguration) VerifyCredential(context.Context, sandbox.DirectConfig, []sandbox.Reference) error {
	return &providercontract.UnsupportedError{Operation: "VerifyCredential", Reason: "not_ready"}
}

func TestSupportedSetupOperationsCannotReportUnsupported(t *testing.T) {
	registry := Builtin()
	a := registry.adapters["docker"]
	requirements := a.Configuration.Requirements()
	supported := providercontract.Support{State: providercontract.Supported}
	requirements.Discovery, requirements.SelectionDiscovery, requirements.CredentialVerification = supported, supported, supported
	a.Configuration = contradictingConfiguration{registrationConfiguration{requirements: requirements}}
	registry.adapters["docker"] = a
	direct := sandbox.DirectConfig{Selection: sandbox.Selection{Provider: "docker"}}
	_, discoverErr := registry.DiscoverConfiguration(t.Context(), "docker", sandbox.ConfigurationDiscoveryInput{}, sandbox.ProcessPaths{})
	_, selectionErr := registry.DiscoverSelection(t.Context(), direct)
	for _, err := range []error{discoverErr, selectionErr, registry.VerifyCredential(t.Context(), direct, nil)} {
		if !errors.Is(err, providercontract.ErrContract) || errors.Is(err, providercontract.ErrUnsupported) {
			t.Fatal(err)
		}
	}
}
