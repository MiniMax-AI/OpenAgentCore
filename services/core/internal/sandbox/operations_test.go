package sandbox_test

import (
	"context"
	"errors"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/providercontract"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/docker"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/e2b"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/microsandbox"
	"reflect"
	"testing"
)

type changedDeclaration struct {
	*docker.Provider
	operations providercontract.Operations
}

func (p *changedDeclaration) ProviderOperations() providercontract.Operations { return p.operations }

type onlyRequired struct{ sandbox.SandboxProvider }

func (*onlyRequired) ProviderOperations() providercontract.Operations { return docker.Operations() }

func TestDeclarationsRejectMissingUnknownAndContradictoryOperations(t *testing.T) {
	for _, mutate := range []struct {
		name   string
		change func(providercontract.Operations)
	}{
		{"identity unsupported", func(o providercontract.Operations) {
			o["ObservationProviderType"] = providercontract.Support{State: providercontract.Unsupported, Reason: "no_identity"}
		}},
		{"resolver unsupported", func(o providercontract.Operations) {
			o["ResolveObservationSource"] = providercontract.Support{State: providercontract.Unsupported, Reason: "no_resolver"}
		}},
		{"omitted", func(o providercontract.Operations) { delete(o, "DeleteRetained") }},
		{"zero", func(o providercontract.Operations) { o["ObserveBatch"] = providercontract.Support{} }},
		{"unknown", func(o providercontract.Operations) {
			o["FutureOperation"] = providercontract.Support{State: providercontract.Supported}
		}},
		{"required unsupported", func(o providercontract.Operations) {
			o["Renew"] = providercontract.Support{State: providercontract.Unsupported, Reason: "no_native_lease"}
		}},
		{"partial checkpoint", func(o providercontract.Operations) {
			o["Initial"] = providercontract.Support{State: providercontract.Supported}
		}},
		{"unsafe reason", func(o providercontract.Operations) {
			o["ObserveBatch"] = providercontract.Support{State: providercontract.Unsupported, Reason: "https://private:key@host"}
		}},
		{"unknown state", func(o providercontract.Operations) {
			o["ObserveBatch"] = providercontract.Support{State: "unavailable"}
		}},
	} {
		t.Run(mutate.name, func(t *testing.T) {
			o := docker.Operations()
			mutate.change(o)
			if err := sandbox.ValidateProvider(&changedDeclaration{Provider: &docker.Provider{}, operations: o}); !errors.Is(err, providercontract.ErrContract) {
				t.Fatal(err)
			}
		})
	}
	if err := sandbox.ValidateProvider(&onlyRequired{}); !errors.Is(err, providercontract.ErrContract) {
		t.Fatal("missing extension implementations accepted", err)
	}
	var nilProvider *docker.Provider
	if err := sandbox.ValidateProvider(nilProvider); !errors.Is(err, providercontract.ErrContract) {
		t.Fatal("typed nil accepted", err)
	}
}

// Unsupported methods must be callable safely even without native clients or
// configuration: any attempt at native I/O would panic on these zero providers.
func TestEveryUnsupportedNativeOperationRejectsWithoutSideEffects(t *testing.T) {
	for _, provider := range []sandbox.SandboxProvider{&docker.Provider{}, &e2b.Provider{}, &microsandbox.Provider{}} {
		if err := sandbox.ValidateProvider(provider); err != nil {
			t.Fatal(err)
		}
		for name, support := range provider.ProviderOperations() {
			if support.State != providercontract.Unsupported {
				continue
			}
			method := reflect.ValueOf(provider).MethodByName(name)
			arguments := make([]reflect.Value, method.Type().NumIn())
			for i := range arguments {
				arguments[i] = reflect.Zero(method.Type().In(i))
			}
			arguments[0] = reflect.ValueOf(context.Background())
			values := method.Call(arguments)
			err, _ := values[len(values)-1].Interface().(error)
			reason, ok := providercontract.UnsupportedReason(err, name)
			if !ok || reason != support.Reason {
				t.Fatalf("%T.%s returned %v", provider, name, err)
			}
			for _, v := range values[:len(values)-1] {
				if !v.IsZero() {
					t.Fatalf("%s fabricated a successful value", name)
				}
			}
		}
	}
}

// An extended interface cannot inherit success through the existing declaration.
type nextContract interface {
	sandbox.SandboxProvider
	NextOperation(context.Context) error
}
type futureProvider struct{ *docker.Provider }

func (*futureProvider) NextOperation(context.Context) error { return nil }
func TestNewContractRequiresAnAuthoredDecision(t *testing.T) {
	for _, p := range []providercontract.Declared{&docker.Provider{}, &futureProvider{&docker.Provider{}}} {
		if err := providercontract.Validate(p, reflect.TypeFor[nextContract]()); !errors.Is(err, providercontract.ErrContract) {
			t.Fatal("new operation inherited a default", err)
		}
	}
}

type invalidObservationIdentity struct{ *docker.Provider }

func (*invalidObservationIdentity) ObservationProviderType() string { return "" }
func TestProviderRegistrationRequiresObservationIdentity(t *testing.T) {
	if err := sandbox.ValidateProvider(&invalidObservationIdentity{&docker.Provider{}}); !errors.Is(err, providercontract.ErrContract) {
		t.Fatal("provider registration accepted empty observation identity", err)
	}
}
