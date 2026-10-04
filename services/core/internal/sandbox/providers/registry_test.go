package providers

import (
	"errors"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/providercontract"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/e2b"
	"github.com/google/uuid"
	"testing"
)

func TestRegistrationOwnsDeploymentPolicy(t *testing.T) {
	registry := Builtin()
	installation := uuid.NewString()
	for _, tc := range []struct {
		kind, mode, namespace string
		idle, retention       int64
		checkpoint            bool
	}{
		{"docker", "nodes", "nodes", 0, 0, false},
		{"microsandbox", "nodes", "nodes", 300, 86400, true},
		{"e2b", "direct", "e2b", 300, 86400, true},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			d, err := registry.Describe(tc.kind, installation)
			if err != nil || d.Mode != tc.mode || d.IdleSeconds != tc.idle || d.RetentionSeconds != tc.retention || d.BackendFingerprint != BackendFingerprint(tc.kind, tc.namespace+":"+installation) {
				t.Fatalf("wrong namespace or defaults: %+v %v", d, err)
			}
			a, err := registry.Lookup(tc.kind)
			checkpoint, checkpointErr := registry.SupportsSuspension(tc.kind)
			isNode, nodeErr := registry.IsNode(tc.kind)
			if err != nil || checkpointErr != nil || nodeErr != nil || checkpoint != tc.checkpoint || isNode != (tc.mode == "nodes") || (a.BuildLocal != nil) != (tc.mode == "nodes") || (a.BuildDirect != nil) != (tc.mode == "direct") {
				t.Fatal("inconsistent construction/capability registration", err, checkpointErr, nodeErr)
			}
		})
	}
	if _, err := registry.Describe("unregistered", installation); !errors.Is(err, ErrUnknownProvider) || !errors.Is(err, sandbox.ErrInvalid) {
		t.Fatal("unknown kind accepted", err)
	}
}

func TestSelectionNormalizationAndCredentialInheritance(t *testing.T) {
	registry := Builtin()
	input := sandbox.Selection{Provider: "e2b", Configuration: &e2b.DeploymentConfiguration{APIKey: "original-key", Template: "runtime:" + uuid.NewString()}}
	normalized, err := registry.Normalize(input)
	if err != nil || normalized.Configuration.(*e2b.DeploymentConfiguration).APIURL != "https://api.e2b.app" || normalized.Configuration.(*e2b.DeploymentConfiguration).Domain != "e2b.app" {
		t.Fatal("defaults not normalized", err)
	}
	if input.Configuration.(*e2b.DeploymentConfiguration).APIURL != "" || input.Configuration.(*e2b.DeploymentConfiguration).Domain != "" {
		t.Fatal("normalization changed request")
	}
	normalized.Resources = sandbox.Resources{CPUs: 4, MemoryMiB: 4096}
	request := input
	request.Configuration = &e2b.DeploymentConfiguration{Template: input.Configuration.(*e2b.DeploymentConfiguration).Template}
	next, err := registry.ResolveChange(request, normalized)
	if err != nil || next.Resources != normalized.Resources || next.Configuration.(*e2b.DeploymentConfiguration).APIKey != "original-key" || next.Configuration.(*e2b.DeploymentConfiguration).APIURL != normalized.Configuration.(*e2b.DeploymentConfiguration).APIURL || next.ReplacesCredential() {
		t.Fatal("omitted values lost committed selection", err)
	}
	request.Configuration.(*e2b.DeploymentConfiguration).CredentialSupplied = true
	if _, err := registry.ResolveChange(request, normalized); !errors.Is(err, sandbox.ErrInvalid) {
		t.Fatal("explicit empty credential silently inherited", err)
	}
	if request.Configuration.(*e2b.DeploymentConfiguration).APIKey != "" || request.Resources != (sandbox.Resources{}) {
		t.Fatal("resolution changed request")
	}
}

func TestNewRegistrationDoesNotNeedCoreDispatchChanges(t *testing.T) {
	registry := Builtin()
	const kind = "contract-test-provider"
	// Registration is test-local: production registrations are fixed, never plugins.
	registry.adapters[kind] = registry.adapters["docker"]
	s, err := registry.Normalize(sandbox.Selection{Provider: kind, DeploymentSpec: validRegistrationSpec()})
	isNode, nodeErr := registry.IsNode(kind)
	checkpoint, checkpointErr := registry.SupportsSuspension(kind)
	if err != nil || nodeErr != nil || checkpointErr != nil || s.Provider != kind || !isNode || checkpoint {
		t.Fatal("new entry did not follow shared boundary", err, nodeErr, checkpointErr)
	}
	d, err := registry.Describe(kind, uuid.NewString())
	if err != nil || d.Mode != "nodes" || d.IdleSeconds != 0 {
		t.Fatal(d, err)
	}
	if _, err := registry.Normalize(sandbox.Selection{Provider: kind, Configuration: &e2b.DeploymentConfiguration{APIKey: "wrong-provider"}}); !errors.Is(err, sandbox.ErrInvalid) {
		t.Fatal("mixed configuration admitted", err)
	}
}

func TestRetainedLimitUsesRegisteredCapabilities(t *testing.T) {
	registry := Builtin()
	const kind = "capacity-test-provider"
	for _, checkpoint := range []bool{false, true} {
		adapter := registry.adapters["docker"]
		if checkpoint {
			adapter = registry.adapters["microsandbox"]
		}
		registry.adapters[kind] = adapter
		for _, retained := range []int{0, 20} {
			want := 10
			if checkpoint {
				want = retained
			}
			if got, err := registry.RetainedLimit(kind, 10, retained); err != nil || got != want {
				t.Fatalf("checkpoint=%v retained=%d: got %d, want %d (%v)", checkpoint, retained, got, want, err)
			}
		}
	}
}

// Capability questions report lookup failures instead of answering false.
func TestCapabilityLookupsReportFailures(t *testing.T) {
	registry := Builtin()
	invalid := registry.adapters["docker"]
	invalid.Operations = nil
	registry.adapters["invalid-registration"] = invalid
	for kind, want := range map[string]error{"unregistered": ErrUnknownProvider, "invalid-registration": providercontract.ErrContract} {
		if _, err := registry.IsNode(kind); !errors.Is(err, want) {
			t.Fatal(kind, "IsNode", err)
		}
		if _, err := registry.SupportsSuspension(kind); !errors.Is(err, want) {
			t.Fatal(kind, "SupportsSuspension", err)
		}
		if _, err := registry.RetainedLimit(kind, 1, 2); !errors.Is(err, want) {
			t.Fatal(kind, "RetainedLimit", err)
		}
	}
}
