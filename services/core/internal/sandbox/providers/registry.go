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
	NodeArtifacts                 []providerassets.Artifact
	Policy                        sandbox.DeploymentPolicy
	Configuration                 sandbox.ConfigurationAdapter
	BuildLocal                    func(Config, LocalOptions, *Built) (func(), error)
	BuildDirect                   func(DirectConfig) (sandbox.SandboxProvider, error)
	Mode                          string
	Operations                    func() providercontract.Operations
	IdleSeconds, RetentionSeconds int64
	ValidateSpecification         func(sandbox.DeploymentSpec) error
	ValidateResources             func(sandbox.Resources) error
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
			Policy:        docker.Policy(), Operations: docker.Operations, Mode: "nodes", BuildLocal: buildDocker,
			ValidateSpecification: docker.ValidateSpecification, ValidateResources: docker.ValidateResources,
			Configuration: nodeConfigurationAdapter{docker.ValidateSpecification},
		},
		"microsandbox": {
			NodeArtifacts: []providerassets.Artifact{nodeProgram, runtimeImage, runtimePolicy,
				{Path: "native/bin/oac-microsandbox-provider", Suffix: "microsandbox-provider", Role: "runtime"},
				{Path: "native/microsandbox/msb", Suffix: "msb", Role: "runtime"},
				{Path: "native/microsandbox/libkrunfw.so.5.6.1", Suffix: "libkrunfw.so.5.6.1", Role: "runtime"}},
			Policy: microsandbox.Policy(), Operations: microsandbox.Operations, Mode: "nodes", BuildLocal: buildMicrosandbox,
			IdleSeconds: 300, RetentionSeconds: 86400,
			ValidateSpecification: microsandbox.ValidateSpecification, ValidateResources: microsandbox.ValidateResources,
			Configuration: nodeConfigurationAdapter{microsandbox.ValidateSpecification},
		},
		"e2b": {
			Policy: e2b.Policy(), Operations: e2b.Operations, Mode: "direct", BuildDirect: buildE2B,
			IdleSeconds: 300, RetentionSeconds: 86400,
			Configuration:         e2b.ConfigurationAdapter{},
			ValidateSpecification: e2b.ValidateSpecification, ValidateResources: e2b.ValidateResources,
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

// IsNode reports whether the provider runs on enrolled sandbox nodes.
func (r *Registry) IsNode(kind string) (bool, error) {
	a, err := r.Lookup(kind)
	if err != nil {
		return false, err
	}
	return a.Mode == "nodes", nil
}

// SupportsSuspension reports whether the provider declares checkpoint suspension.
func (r *Registry) SupportsSuspension(kind string) (bool, error) {
	a, err := r.Lookup(kind)
	if err != nil {
		return false, err
	}
	return a.Operations()["Initial"].State == providercontract.Supported, nil
}

// RetainedLimit keeps nodes without checkpoint support within their active capacity.
func (r *Registry) RetainedLimit(kind string, active, retained int) (int, error) {
	a, err := r.Lookup(kind)
	if err != nil {
		return 0, err
	}
	if a.Mode == "nodes" && a.Operations()["Initial"].State != providercontract.Supported {
		return active, nil
	}
	return retained, nil
}

func (r *Registry) ValidateSpecification(kind string, s sandbox.DeploymentSpec) error {
	a, e := r.Lookup(kind)
	if e != nil {
		return e
	}
	return a.ValidateSpecification(s)
}
func (r *Registry) ValidateResources(kind string, s sandbox.Resources) error {
	a, e := r.Lookup(kind)
	if e != nil {
		return e
	}
	return a.ValidateResources(s)
}

// Describe derives the description once for both preview and persistence.
// Its fingerprint identifies a namespace, never mutable capacity or a credential.
func (r *Registry) Describe(kind, installation string) (sandbox.Description, error) {
	a, e := r.Lookup(kind)
	if e != nil {
		return sandbox.Description{}, e
	}
	namespace := a.Mode
	if a.Mode == "direct" {
		namespace = kind
	}
	return sandbox.Description{Mode: a.Mode, BackendFingerprint: BackendFingerprint(kind, namespace+":"+installation), IdleSeconds: a.IdleSeconds, RetentionSeconds: a.RetentionSeconds}, nil
}

// PythonDeploymentContract projects the same registered adapter policies into
// the node installer; no second provider list exists in another language.
func (r *Registry) PythonDeploymentContract() (string, error) {
	policies := make(map[string]sandbox.DeploymentPolicy, len(r.adapters))
	for kind := range r.adapters {
		a, err := r.Lookup(kind)
		if err != nil {
			return "", err
		}
		policies[kind] = a.Policy
	}
	return sandbox.PythonDeploymentContract(policies), nil
}
