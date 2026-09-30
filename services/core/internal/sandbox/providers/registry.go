// Package providers is the explicit registration boundary for sandbox adapters.
// It performs read-only configuration work; it never allocates compute.
package providers

import (
	"crypto/sha256"
	"encoding/hex"
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
	BuildLocal                    func(Config, *Built) (func(), error)
	BuildDirect                   func(DirectConfig) (sandbox.SandboxProvider, error)
	Mode                          string
	Operations                    func() providercontract.Operations
	IdleSeconds, RetentionSeconds int64
	ValidateSpecification         func(sandbox.DeploymentSpec) error
	ValidateResources             func(sandbox.Resources) error
}

var adapters = map[string]Adapter{
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
		Configuration:         e2b.ConfigurationAdapter{},
		ValidateSpecification: e2b.ValidateSpecification, ValidateResources: e2b.ValidateResources,
	},
}

func Lookup(kind string) (Adapter, error) {
	a, ok := adapters[kind]
	if !ok {
		return Adapter{}, fmt.Errorf("%w: unsupported sandbox provider", sandbox.ErrInvalid)
	}
	if err := ValidateRegistration(a); err != nil {
		return Adapter{}, err
	}
	return a, nil
}
func IsNode(kind string) bool { a, e := Lookup(kind); return e == nil && a.Mode == "nodes" }
func SupportsCheckpoint(kind string) bool {
	a, e := Lookup(kind)
	return e == nil && a.Operations()["Initial"].State == providercontract.Supported
}

// RetainedLimit keeps nodes without checkpoint support within their active capacity.
func RetainedLimit(kind string, active, retained int) int {
	a, err := Lookup(kind)
	if err == nil && a.Mode == "nodes" && !SupportsCheckpoint(kind) {
		return active
	}
	return retained
}

func ValidateSpecification(kind string, s sandbox.DeploymentSpec) error {
	a, e := Lookup(kind)
	if e != nil {
		return e
	}
	return a.ValidateSpecification(s)
}
func ValidateResources(kind string, s sandbox.Resources) error {
	a, e := Lookup(kind)
	if e != nil {
		return e
	}
	return a.ValidateResources(s)
}

// Description is derived once for both preview and persistence. Its fingerprint
// identifies a namespace, never mutable capacity or a credential.
type Description struct {
	Mode, BackendFingerprint      string
	IdleSeconds, RetentionSeconds int64
}

func Describe(kind, installation string) (Description, error) {
	a, e := Lookup(kind)
	if e != nil {
		return Description{}, e
	}
	namespace := a.Mode
	if a.Mode == "direct" {
		namespace = kind
	}
	digest := sha256.Sum256([]byte(kind + "\x00" + namespace + ":" + installation))
	return Description{a.Mode, hex.EncodeToString(digest[:]), a.IdleSeconds, a.RetentionSeconds}, nil
}

// PythonDeploymentContract projects the same registered adapter policies into
// the node installer; no second provider list exists in another language.
func PythonDeploymentContract() (string, error) {
	policies := make(map[string]sandbox.DeploymentPolicy, len(adapters))
	for kind := range adapters {
		a, err := Lookup(kind)
		if err != nil {
			return "", err
		}
		policies[kind] = a.Policy
	}
	return sandbox.PythonDeploymentContract(policies), nil
}
