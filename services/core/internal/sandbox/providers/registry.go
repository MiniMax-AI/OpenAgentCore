// Package providers is the explicit registration boundary for sandbox adapters.
// It performs read-only configuration work; it never allocates compute.
package providers

import (
	"fmt"

	"github.com/MiniMax-AI/OpenAgentCore/internal/providerassets"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/providercontract"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/docker"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/e2b"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/microsandbox"
)

// Adapter describes configuration and transport independently of compute operations.
// Native operation support comes from the adapter-owned complete declaration.
type Adapter struct {
	NodeArtifacts []providerassets.Artifact
	Policy        sandbox.DeploymentPolicy
	Setup         sandbox.SetupDeclaration
	Configuration sandbox.ConfigurationAdapter
	BuildLocal    func(sandbox.NodeConfig, sandbox.LocalOptions, *sandbox.Built) (func(), error)
	BuildDirect   func(sandbox.DirectConfig) (sandbox.SandboxProvider, error)
	Mode          sandbox.DeploymentMode
	Operations    func() providercontract.Operations
}

// Registry holds the registered adapters. Core and the node program each build
// one with Builtin and pass it explicitly; there is no package-level lookup.
type Registry struct{ adapters map[string]Adapter }

// ErrUnknownProvider reports a provider kind that no adapter registers.
var ErrUnknownProvider = fmt.Errorf("%w: unsupported sandbox provider", sandbox.ErrInvalid)

// Builtin returns a registry of the adapters built into this program.
func Builtin() *Registry {
	return &Registry{adapters: map[string]Adapter{
		"docker": {
			NodeArtifacts: []providerassets.Artifact{nodeProgram, runtimeImage, runtimePolicy},
			Policy:        docker.Policy(), Setup: docker.Setup(), Operations: docker.Operations, Mode: sandbox.DeploymentNodes, BuildLocal: docker.BuildNode,
			Configuration: nodeConfigurationAdapter{"docker", docker.Policy()},
		},
		"microsandbox": {
			NodeArtifacts: append([]providerassets.Artifact{nodeProgram, runtimeImage, runtimePolicy}, microsandbox.NodeArtifacts...),
			Policy:        microsandbox.Policy(), Setup: microsandbox.Setup(), Operations: microsandbox.Operations, Mode: sandbox.DeploymentNodes, BuildLocal: microsandbox.BuildNode,
			Configuration: nodeConfigurationAdapter{"microsandbox", microsandbox.Policy()},
		},
		"e2b": {
			Policy: e2b.Policy(), Setup: e2b.Setup(), Operations: e2b.Operations, Mode: sandbox.DeploymentDirect, BuildDirect: e2b.BuildDirect,
			Configuration: e2b.ConfigurationAdapter{},
		},
	}}
}

// Lookup returns a registered adapter after validating its registration.
func (r *Registry) Lookup(kind string) (Adapter, error) {
	a, ok := r.adapters[kind]
	if !ok {
		return Adapter{}, ErrUnknownProvider
	}
	if err := ValidateRegistration(a); err != nil {
		return Adapter{}, err
	}
	return a, nil
}

// BuildDirect builds a direct-mode Provider and validates its binding.
func (r *Registry) BuildDirect(c sandbox.DirectConfig) (sandbox.SandboxProvider, error) {
	a, e := r.Lookup(c.Selection.Provider)
	if e != nil {
		return nil, e
	}
	if a.BuildDirect == nil {
		return nil, sandbox.ErrInvalid
	}
	p, err := a.BuildDirect(c)
	if err != nil {
		return nil, err
	}
	if err := ValidateBinding(a, p); err != nil {
		return nil, err
	}
	return p, nil
}

// SupportsCheckpoint reports whether the provider declares checkpoint suspension.
func (r *Registry) SupportsCheckpoint(kind string) (bool, error) {
	a, err := r.Lookup(kind)
	if err != nil {
		return false, err
	}
	return sandbox.SupportsCheckpoint(a.Operations()), nil
}

// RetainedLimit keeps nodes without checkpoint support within their active capacity.
func (r *Registry) RetainedLimit(kind string, active, retained int) (int, error) {
	a, err := r.Lookup(kind)
	if err != nil {
		return 0, err
	}
	if a.Mode == sandbox.DeploymentNodes && !sandbox.SupportsCheckpoint(a.Operations()) {
		return active, nil
	}
	return retained, nil
}

func (r *Registry) ValidateSpecification(kind string, s sandbox.DeploymentSpec) error {
	a, e := r.Lookup(kind)
	if e != nil {
		return e
	}
	return s.ValidatePolicy(kind, a.Policy)
}

// Describe derives the description once for both preview and persistence.
// Its fingerprint identifies a namespace, never mutable capacity or a credential.
func (r *Registry) Describe(kind, installation string) (sandbox.Description, error) {
	a, e := r.Lookup(kind)
	if e != nil {
		return sandbox.Description{}, e
	}
	namespace := string(a.Mode)
	if a.Mode == sandbox.DeploymentDirect {
		namespace = kind
	}
	return sandbox.Description{Mode: a.Mode, BackendFingerprint: sandbox.BackendFingerprint(kind, namespace+":"+installation)}, nil
}

// DeploymentContract projects the registered modes, policies and setup
// declarations into the node installer and the TypeScript client; no second
// provider list exists in another language. At most one Provider per mode is
// recommended.
func (r *Registry) DeploymentContract() (python, typescript string, err error) {
	providers := make(map[string]sandbox.ProviderProjection, len(r.adapters))
	recommended := map[sandbox.DeploymentMode]bool{}
	for kind := range r.adapters {
		a, err := r.Lookup(kind)
		if err != nil {
			return "", "", err
		}
		if a.Setup.Recommended {
			if recommended[a.Mode] {
				return "", "", fmt.Errorf("%w: several recommended %s providers", providercontract.ErrContract, a.Mode)
			}
			recommended[a.Mode] = true
		}
		requirements := a.Configuration.Requirements()
		setup := a.Setup
		setup.Cautions, setup.Fields, setup.Presets = nonNil(setup.Cautions), nonNil(setup.Fields), nonNil(setup.Presets)
		providers[kind] = sandbox.ProviderProjection{Mode: a.Mode, DeploymentPolicy: a.Policy, Setup: &sandbox.ProviderSetup{SetupDeclaration: setup,
			Credential: requirements.Credential, PublicOrigin: requirements.PublicOrigin, Discovery: requirements.Discovery.State == providercontract.Supported}}
	}
	python, typescript = sandbox.DeploymentContract(providers)
	return python, typescript, nil
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
