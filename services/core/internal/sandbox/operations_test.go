package sandbox_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/providercontract"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/contracttest"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/docker"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/e2b"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/microsandbox"
)

type changedDeclaration struct {
	*docker.Provider
	operations providercontract.Operations
}

func (p *changedDeclaration) ProviderOperations() providercontract.Operations { return p.operations }

func TestDeclarationsRejectMissingUnknownAndContradictoryOperations(t *testing.T) {
	for _, mutate := range []struct {
		name   string
		change func(providercontract.Operations)
	}{
		{"omitted", func(o providercontract.Operations) { delete(o, "DeleteSnapshot") }},
		{"zero", func(o providercontract.Operations) { o["Observe"] = providercontract.Support{} }},
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
			o["Observe"] = providercontract.Support{State: providercontract.Unsupported, Reason: "https://private:key@host"}
		}},
		{"unknown state", func(o providercontract.Operations) {
			o["Observe"] = providercontract.Support{State: "unavailable"}
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

// Bootstrap.Validate is the one gate Core applies before Create.
func TestBootstrapValidateRejections(t *testing.T) {
	ref := sandbox.Reference{TenantID: uuid.NewString(), EnvironmentID: uuid.NewString(), AllocationID: uuid.NewString()}
	if err := contracttest.Bootstrap(ref).Validate(); err != nil {
		t.Fatal("valid bootstrap rejected:", err)
	}
	for name, change := range map[string]func(*sandbox.Bootstrap){
		"noncanonical tenant":        func(b *sandbox.Bootstrap) { b.TenantID = strings.ToUpper(b.TenantID) },
		"plain ws Link off loopback": func(b *sandbox.Bootstrap) { b.SandboxIO.LinkURL = "ws://core.example/api/v1/sandbox-link" },
		"empty Serve credential":     func(b *sandbox.Bootstrap) { b.SandboxIO.Credential = "" },
		"zero generation":            func(b *sandbox.Bootstrap) { b.SandboxIO.Resource.Generation = 0 },
		"enrollment resource":        func(b *sandbox.Bootstrap) { b.SandboxIO.Resource.Kind = "enrollment" },
		"another allocation":         func(b *sandbox.Bootstrap) { b.SandboxIO.Resource.ID = uuid.NewString() },
		"another tenant":             func(b *sandbox.Bootstrap) { b.SandboxIO.Resource.TenantID = uuid.NewString() },
		"another Environment":        func(b *sandbox.Bootstrap) { b.SandboxIO.Resource.EnvironmentID = uuid.NewString() },
	} {
		b := contracttest.Bootstrap(ref)
		change(&b)
		if err := b.Validate(); !errors.Is(err, sandbox.ErrInvalid) {
			t.Errorf("%s: Validate = %v", name, err)
		}
	}
}
