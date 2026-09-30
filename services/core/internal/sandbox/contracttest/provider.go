// Package contracttest runs shared lifecycle assertions against real adapters
// backed by controlled native transports. It is test support only.
package contracttest

import (
	"context"
	"errors"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/providercontract"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"reflect"
	"testing"
	"time"
)

type Fault string

const (
	TransportFailure Fault = "transport_failure"
	Canceled         Fault = "canceled_after_dispatch"
	ForeignOwnership Fault = "foreign_ownership"
	CleanupFailure   Fault = "cleanup_failure"
)

type Scenario struct {
	Operation string
	Fault     Fault
}

// Fixture exposes the production adapter. Calls records all native requests;
// WantCalls includes legitimate multi-step work before the injected fault. Exact
// comparison detects replay, fallback and implicit cleanup after uncertain Create.
type Fixture struct {
	Provider  sandbox.SandboxProvider
	Bootstrap sandbox.Bootstrap
	Calls     func() []string
	WantCalls []string
}

// Factory configures native responses. Invoke cancel only after dispatch begins.
type Factory func(t *testing.T, scenario Scenario, cancel context.CancelFunc) Fixture

// RunFailures requires errors without mandating one native error type. Failed
// or canceled calls must not invent proof of settlement or bootstrap completion.
func RunFailures(t *testing.T, factory Factory) {
	t.Helper()
	for _, operation := range []string{"create", "inspect", "renew", "kill"} {
		faults := []Fault{TransportFailure, Canceled, ForeignOwnership}
		if operation == "kill" {
			faults = append(faults, CleanupFailure)
		}
		for _, fault := range faults {
			t.Run(operation+"/"+string(fault), func(t *testing.T) {
				ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
				defer cancel()
				f := factory(t, Scenario{operation, fault}, cancel)
				if err := sandbox.ValidateProvider(f.Provider); err != nil {
					t.Fatal(err)
				}
				var info sandbox.Info
				var err error
				switch operation {
				case "create":
					info, err = f.Provider.Create(ctx, f.Bootstrap)
				case "inspect":
					info, err = f.Provider.GetInfo(ctx, f.Bootstrap.Reference)
				case "renew":
					info, err = f.Provider.Renew(ctx, f.Bootstrap.Reference)
				case "kill":
					err = f.Provider.Kill(ctx, f.Bootstrap.Reference)
				}
				if errors.Is(err, providercontract.ErrUnsupported) {
					t.Fatal("required operation returned Unsupported", err)
				}
				if err == nil {
					t.Fatal("native failure was reported as success")
				}
				if fault == Canceled && ctx.Err() == nil {
					t.Fatal("fixture did not cancel the dispatched operation")
				}
				if info.CreateSettled || info.BootstrapComplete {
					t.Fatalf("failed operation invented settlement or bootstrap proof: %+v", info)
				}
				if info.Reference != (sandbox.Reference{}) && info.Reference != f.Bootstrap.Reference {
					t.Fatalf("failure exposed a foreign reference: %+v", info.Reference)
				}
				if got := f.Calls(); !reflect.DeepEqual(got, f.WantCalls) {
					t.Fatalf("native calls = %v, want %v; unexpected replay, mutation or cleanup", got, f.WantCalls)
				}
			})
		}
	}
}

// AssertObservation checks identity and the expected native state, without
// equating a running process with authenticated Runtime or executor readiness.
func AssertObservation(t *testing.T, got sandbox.Info, err error, reference sandbox.Reference, providerID, state string) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	if got.Reference != reference || got.ProviderID == "" || providerID != "" && got.ProviderID != providerID || got.State != state {
		t.Fatalf("observation lost identity or native state: %+v", got)
	}
}
