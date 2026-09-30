package providers

import (
	"errors"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/providercontract"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/docker"
	"github.com/google/uuid"
)

func validRegistrationSpec() sandbox.DeploymentSpec {
	return sandbox.DeploymentSpec{
		Resources: sandbox.Resources{CPUs: 2, MemoryMiB: 2048},
		Runtime: &sandbox.RuntimeRelease{
			SourceCommit: strings.Repeat("a", 40), ImageID: "sha256:" + strings.Repeat("b", 64),
			ImageManifestDigest: "sha256:" + strings.Repeat("c", 64),
			MicrosandboxRef:     "oac-runtime@sha256:" + strings.Repeat("d", 64),
			RuntimeSHA256:       strings.Repeat("e", 64), FirmwareSHA256: strings.Repeat("f", 64),
		},
	}
}

func TestRegistrationRejectsBeforeCallbacksOrConstruction(t *testing.T) {
	registry := Builtin()
	const kind = "registration-test-provider"
	for _, tc := range []struct {
		name   string
		mutate func(*Adapter)
	}{
		{"missing Runtime input policy", func(a *Adapter) { a.Policy = sandbox.DeploymentPolicy{} }},
		{"contradictory Runtime input policy", func(a *Adapter) { a.Policy.RuntimeError = "Runtime rejected" }},
		{"missing mode", func(a *Adapter) { a.Mode = "" }},
		{"unknown mode", func(a *Adapter) { a.Mode = "private-token" }},
		{"missing local constructor", func(a *Adapter) { a.BuildLocal = nil }},
		{"wrong local constructor", func(a *Adapter) { a.Mode = "direct" }},
		{"missing direct constructor", func(a *Adapter) { a.Mode = "direct"; a.BuildLocal = nil }},
		{"both constructors", func(a *Adapter) {
			a.BuildDirect = func(DirectConfig) (sandbox.SandboxProvider, error) {
				t.Fatal("called direct constructor")
				return nil, nil
			}
		}},
		{"missing specification validator", func(a *Adapter) { a.ValidateSpecification = nil }},
		{"missing resource validator", func(a *Adapter) { a.ValidateResources = nil }},
		{"missing configuration", func(a *Adapter) { a.Configuration = nil }},
		{"typed nil configuration", func(a *Adapter) { var c *registrationConfiguration; a.Configuration = c }},
		{"missing discovery implementation", func(a *Adapter) { a.Configuration = missingConfigurationDiscovery{a.Configuration} }},
		{"missing configuration requirement", func(a *Adapter) { a.Configuration = registrationConfiguration{} }},
		{"invalid credential requirement", func(a *Adapter) {
			r := a.Configuration.Requirements()
			r.Credential = "private-token"
			a.Configuration = registrationConfiguration{requirements: r}
		}},
		{"invalid public origin requirement", func(a *Adapter) {
			r := a.Configuration.Requirements()
			r.PublicOrigin = "private-token"
			a.Configuration = registrationConfiguration{requirements: r}
		}},

		{"missing operations", func(a *Adapter) { a.Operations = nil }},
		{"incomplete operations", func(a *Adapter) { a.Operations = func() providercontract.Operations { return nil } }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := registry.adapters["docker"]
			a.BuildLocal = func(Config, LocalOptions, *Built) (func(), error) {
				t.Fatal("called local constructor")
				return nil, nil
			}
			a.ValidateSpecification = func(sandbox.DeploymentSpec) error { t.Fatal("called specification validator"); return nil }
			a.ValidateResources = func(sandbox.Resources) error { t.Fatal("called resource validator"); return nil }
			a.Configuration = registrationConfiguration{requirements: a.Configuration.Requirements()}
			tc.mutate(&a)
			registry.adapters[kind] = a
			selection := sandbox.Selection{Provider: kind, DeploymentSpec: validRegistrationSpec()}
			for _, entry := range []struct {
				name string
				call func() error
			}{
				{"lookup", func() error { _, err := registry.Lookup(kind); return err }},
				{"credential requirement", func() error { _, err := registry.UsesCredential(kind); return err }},
				{"public origin requirement", func() error { _, err := registry.RequiresPublicOrigin(kind); return err }},
				{"normalize", func() error { _, err := registry.Normalize(selection); return err }},
				{"specification", func() error { return registry.ValidateSpecification(kind, selection.DeploymentSpec) }},
				{"resources", func() error { return registry.ValidateResources(kind, selection.Resources) }},
				{"description", func() error { _, err := registry.Describe(kind, uuid.NewString()); return err }},
				{"decode input", func() error { _, err := registry.DecodeInput(kind, nil, nil); return err }},
				{"encode", func() error { _, err := registry.Encode(kind, nil); return err }},
				{"decode", func() error { _, err := registry.Decode(kind, sandbox.ConfigurationRecord{}); return err }},
				{"equal", func() error { _, err := registry.Equal(kind, nil, nil); return err }},
				{"discovery", func() error {
					_, err := registry.DiscoverConfiguration(t.Context(), kind, sandbox.ConfigurationDiscoveryInput{}, sandbox.ProcessPaths{})
					return err
				}},
				{"resolve change", func() error { _, err := registry.ResolveChange(selection, selection); return err }},
				{"credential", func() error { _, err := registry.WithCredential(selection, selection); return err }},
				{"local build", func() error {
					_, _, err := registry.Build(Config{Provider: kind, Generation: 1, InstallationID: uuid.NewString(), Specification: selection.DeploymentSpec}, LocalOptions{Standalone: true})
					return err
				}},
				{"direct build", func() error { _, err := registry.BuildDirect(DirectConfig{Selection: selection}); return err }},
				{"binding", func() error { return ValidateBinding(a, &docker.Provider{}) }},
				{"projection", func() error {
					text, err := registry.PythonDeploymentContract()
					if text != "" {
						t.Fatal("partial invalid projection")
					}
					return err
				}},
			} {
				t.Run(entry.name, func(t *testing.T) {
					err := entry.call()
					if !errors.Is(err, providercontract.ErrContract) || strings.Contains(err.Error(), "private-token") {
						t.Fatalf("expected safe registration error, got %v", err)
					}
				})
			}
		})
	}
}

func TestCompleteRegistrationsPreserveConstruction(t *testing.T) {
	registry := Builtin()
	for kind, a := range registry.adapters {
		if err := ValidateRegistration(a); err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
	}
	const kind = "new-test-provider"
	calls, closes := 0, 0
	options := LocalOptions{GenerationStateDirectory: t.TempDir()}
	a := registry.adapters["docker"]
	a.BuildLocal = func(_ Config, got LocalOptions, built *Built) (func(), error) {
		calls++
		if got != options {
			t.Fatalf("construction options = %+v, want %+v", got, options)
		}
		built.Provider = &docker.Provider{}
		return func() { closes++ }, nil
	}
	registry.adapters[kind] = a
	built, closeProvider, err := registry.Build(Config{Provider: kind, Generation: 1, InstallationID: uuid.NewString(), Specification: validRegistrationSpec()}, options)
	if err != nil || built.Provider == nil || calls != 1 {
		t.Fatalf("node build: %v calls=%d", err, calls)
	}
	closeProvider()
	if closes != 1 {
		t.Fatalf("provider close calls = %d", closes)
	}
	// Direct providers may legitimately need no remote credential or extra
	// selection state; registration must not require irrelevant callback stubs.
	a.Mode, a.BuildLocal, a.NodeArtifacts = "direct", nil, nil
	a.BuildDirect = func(DirectConfig) (sandbox.SandboxProvider, error) {
		calls++
		return &docker.Provider{}, nil
	}
	registry.adapters[kind] = a
	p, err := registry.BuildDirect(DirectConfig{Selection: sandbox.Selection{Provider: kind}})
	if err != nil || p == nil || calls != 2 {
		t.Fatalf("credential-free direct build: %v calls=%d", err, calls)
	}
	if _, err := registry.PythonDeploymentContract(); err != nil {
		t.Fatal(err)
	}
}

// Idle time is measured before suspension, retention after suspension. Neither
// duration needs to be greater than the other.
func TestRegistrationCheckpointPolicy(t *testing.T) {
	registry := Builtin()
	for _, tc := range []struct {
		name            string
		kind            string
		idle, retention int64
		direct, valid   bool
	}{
		{"negative idle", "microsandbox", -1, 20, false, false},
		{"missing idle", "microsandbox", 0, 20, false, false},
		{"missing retention", "microsandbox", 20, 0, false, false},
		{"overflow", "microsandbox", 1<<63 - 1, 20, false, false},
		{"direct suspension", "microsandbox", 20, 20, true, false},
		{"unsupported suspension", "docker", 20, 20, false, false},
		{"independent durations", "microsandbox", 300, 30, false, true},
		{"no suspension", "docker", 0, 0, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := registry.adapters[tc.kind]
			a.IdleSeconds, a.RetentionSeconds = tc.idle, tc.retention
			if tc.direct {
				a.Mode, a.BuildLocal, a.BuildDirect = "direct", nil, registry.adapters["e2b"].BuildDirect
			}
			err := ValidateRegistration(a)
			if (err == nil) != tc.valid || err != nil && !errors.Is(err, providercontract.ErrContract) {
				t.Fatal(err)
			}
		})
	}
}
