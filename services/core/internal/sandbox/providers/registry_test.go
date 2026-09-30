package providers

import (
	"errors"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/docker"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/e2b"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/microsandbox"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/google/uuid"
)

func TestRegistrationOwnsDeploymentPolicy(t *testing.T) {
	installation := uuid.NewString()
	for _, tc := range []struct {
		kind, mode, namespace string
		idle, retention       int64
		checkpoint            bool
	}{
		{"docker", "nodes", "nodes", 0, 0, false},
		{"microsandbox", "nodes", "nodes", 300, 86400, true},
		{"e2b", "direct", "e2b", 0, 0, false},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			d, err := Describe(tc.kind, installation)
			if err != nil || d.Mode != tc.mode || d.IdleSeconds != tc.idle || d.RetentionSeconds != tc.retention || d.BackendFingerprint != BackendFingerprint(tc.kind, tc.namespace+":"+installation) {
				t.Fatalf("wrong namespace or defaults: %+v %v", d, err)
			}
			a, err := Lookup(tc.kind)
			if err != nil || SupportsCheckpoint(tc.kind) != tc.checkpoint || IsNode(tc.kind) != (tc.mode == "nodes") || (a.BuildLocal != nil) != (tc.mode == "nodes") || (a.BuildDirect != nil) != (tc.mode == "direct") {
				t.Fatal("inconsistent construction/capability registration", err)
			}
		})
	}
	if _, err := Describe("unregistered", installation); !errors.Is(err, sandbox.ErrInvalid) {
		t.Fatal("unknown kind accepted", err)
	}
}

func TestSelectionNormalizationAndCredentialInheritance(t *testing.T) {
	input := sandbox.Selection{Provider: "e2b", Configuration: &e2b.DeploymentConfiguration{APIKey: "original-key", Template: "runtime:" + uuid.NewString()}}
	normalized, err := Normalize(input)
	if err != nil || normalized.Configuration.(*e2b.DeploymentConfiguration).APIURL != "https://api.e2b.app" || normalized.Configuration.(*e2b.DeploymentConfiguration).Domain != "e2b.app" {
		t.Fatal("defaults not normalized", err)
	}
	if input.Configuration.(*e2b.DeploymentConfiguration).APIURL != "" || input.Configuration.(*e2b.DeploymentConfiguration).Domain != "" {
		t.Fatal("normalization changed request")
	}
	normalized.Resources = sandbox.Resources{CPUs: 4, MemoryMiB: 4096}
	request := input
	request.Configuration = &e2b.DeploymentConfiguration{Template: input.Configuration.(*e2b.DeploymentConfiguration).Template}
	next, err := ResolveChange(request, normalized)
	if err != nil || next.Resources != normalized.Resources || next.Configuration.(*e2b.DeploymentConfiguration).APIKey != "original-key" || next.Configuration.(*e2b.DeploymentConfiguration).APIURL != normalized.Configuration.(*e2b.DeploymentConfiguration).APIURL || next.ReplacesCredential() {
		t.Fatal("omitted values lost committed selection", err)
	}
	request.Configuration.(*e2b.DeploymentConfiguration).CredentialSupplied = true
	if _, err := ResolveChange(request, normalized); !errors.Is(err, sandbox.ErrInvalid) {
		t.Fatal("explicit empty credential silently inherited", err)
	}
	if request.Configuration.(*e2b.DeploymentConfiguration).APIKey != "" || request.Resources != (sandbox.Resources{}) {
		t.Fatal("resolution changed request")
	}
}

func TestNewRegistrationDoesNotNeedCoreDispatchChanges(t *testing.T) {
	const kind = "contract-test-provider"
	// Registration is test-local: production registrations are fixed, never plugins.
	adapters[kind] = Adapter{Mode: "nodes", Operations: docker.Operations, ValidateSpecification: func(sandbox.DeploymentSpec) error { return nil }, Configuration: nodeConfigurationAdapter{validate: func(sandbox.DeploymentSpec) error { return nil }}}
	defer delete(adapters, kind)
	s, err := Normalize(sandbox.Selection{Provider: kind})
	if err != nil || s.Provider != kind || !IsNode(kind) || SupportsCheckpoint(kind) {
		t.Fatal("new entry did not follow shared boundary", err)
	}
	d, err := Describe(kind, uuid.NewString())
	if err != nil || d.Mode != "nodes" || d.IdleSeconds != 0 {
		t.Fatal(d, err)
	}
	if _, err := Normalize(sandbox.Selection{Provider: kind, Configuration: &e2b.DeploymentConfiguration{APIKey: "wrong-provider"}}); !errors.Is(err, sandbox.ErrInvalid) {
		t.Fatal("mixed configuration admitted", err)
	}
}

func TestRetainedLimitUsesRegisteredCapabilities(t *testing.T) {
	const kind = "capacity-test-provider"
	defer delete(adapters, kind)
	for _, checkpoint := range []bool{false, true} {
		operations := docker.Operations
		if checkpoint {
			operations = microsandbox.Operations
		}
		adapters[kind] = Adapter{Mode: "nodes", Operations: operations}
		for _, retained := range []int{0, 20} {
			want := 10
			if checkpoint {
				want = retained
			}
			if got := RetainedLimit(kind, 10, retained); got != want {
				t.Fatalf("checkpoint=%v retained=%d: got %d, want %d", checkpoint, retained, got, want)
			}
		}
	}
}
