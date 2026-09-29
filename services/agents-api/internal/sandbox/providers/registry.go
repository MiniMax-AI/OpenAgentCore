// Package providers is the explicit registration boundary for sandbox adapters.
// It performs read-only configuration work; it never allocates compute.
package providers

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox/docker"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox/e2b"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox/microsandbox"
)

// Adapter describes configuration and transport independently of compute operations.
// Optional native operations remain interface assertions on SandboxProvider.
type Adapter struct {
	Policy                        sandbox.DeploymentPolicy
	ReplaceCredential             func(sandbox.Selection, sandbox.Selection) sandbox.Selection
	CredentialRequiresReset       func(error) bool
	CredentialUnconfirmed         error
	Restore                       func(sandbox.Selection) (sandbox.Selection, error)
	ResolveChange                 func(sandbox.Selection, sandbox.Selection) sandbox.Selection
	BuildLocal                    func(Config, *Built) (func(), error)
	BuildDirect                   func(DirectConfig) (sandbox.SandboxProvider, error)
	Credential                    bool
	PublicOrigin                  bool
	Mode                          string
	Checkpoint                    bool
	IdleSeconds, RetentionSeconds int64
	ValidateSpecification         func(sandbox.DeploymentSpec) error
	ValidateResources             func(sandbox.Resources) error
	Normalize                     func(sandbox.Selection) (sandbox.Selection, error)
}

var adapters = map[string]Adapter{
	"docker": {
		Policy: docker.Policy(), Mode: "nodes", BuildLocal: buildDocker,
		ValidateSpecification: docker.ValidateSpecification, ValidateResources: docker.ValidateResources,
		Normalize: nodeSelection(docker.ValidateSpecification),
	},
	"microsandbox": {
		Policy: microsandbox.Policy(), Mode: "nodes", BuildLocal: buildMicrosandbox,
		Checkpoint: true, IdleSeconds: 300, RetentionSeconds: 86400,
		ValidateSpecification: microsandbox.ValidateSpecification, ValidateResources: microsandbox.ValidateResources,
		Normalize: nodeSelection(microsandbox.ValidateSpecification),
	},
	"e2b": {
		Policy: e2b.Policy(), Mode: "direct", BuildDirect: buildE2B,
		Credential: true, PublicOrigin: true,
		ReplaceCredential: e2b.ReplaceCredential, CredentialRequiresReset: e2b.CredentialRequiresReset,
		CredentialUnconfirmed: e2b.ErrRequestUnconfirmed, Restore: e2b.RestoreSelection,
		ResolveChange: e2b.ResolveChange, Normalize: e2b.NormalizeSelection,
		ValidateSpecification: e2b.ValidateSpecification, ValidateResources: e2b.ValidateResources,
	},
}

func Lookup(kind string) (Adapter, error) {
	a, ok := adapters[kind]
	if !ok {
		return Adapter{}, fmt.Errorf("%w: unsupported sandbox provider", sandbox.ErrInvalid)
	}
	return a, nil
}
func IsNode(kind string) bool             { a, e := Lookup(kind); return e == nil && a.Mode == "nodes" }
func SupportsCheckpoint(kind string) bool { a, e := Lookup(kind); return e == nil && a.Checkpoint }

// RetainedLimit keeps nodes without checkpoint support within their active capacity.
func RetainedLimit(kind string, active, retained int) int {
	a, err := Lookup(kind)
	if err == nil && a.Mode == "nodes" && !a.Checkpoint {
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
func Normalize(s sandbox.Selection) (sandbox.Selection, error) {
	a, e := Lookup(s.Provider)
	if e != nil {
		return s, e
	}
	return a.Normalize(s)
}
func nodeSelection(validate func(sandbox.DeploymentSpec) error) func(sandbox.Selection) (sandbox.Selection, error) {
	return func(s sandbox.Selection) (sandbox.Selection, error) {
		if s.E2B != nil {
			return s, sandbox.ErrInvalid
		}
		return s, validate(s.DeploymentSpec)
	}
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

func UsesCredential(kind string) bool { a, e := Lookup(kind); return e == nil && a.Credential }
func Restore(s sandbox.Selection) (sandbox.Selection, error) {
	a, e := Lookup(s.Provider)
	if e != nil {
		return s, e
	}
	if a.Restore != nil {
		return a.Restore(s)
	}
	s.E2B = nil
	return s, nil
}
func ResolveChange(next, previous sandbox.Selection) (sandbox.Selection, error) {
	a, e := Lookup(next.Provider)
	if e != nil {
		return next, e
	}
	if a.ResolveChange != nil {
		next = a.ResolveChange(next, previous)
	}
	return Normalize(next)
}

func WithCredential(owner, candidate sandbox.Selection) (sandbox.Selection, error) {
	a, e := Lookup(owner.Provider)
	if e != nil {
		return owner, e
	}
	if a.ReplaceCredential == nil {
		return owner, sandbox.ErrInvalid
	}
	return a.ReplaceCredential(owner, candidate), nil
}

// PythonDeploymentContract projects the same registered adapter policies into
// the node installer; no second provider list exists in another language.
func PythonDeploymentContract() string {
	policies := make(map[string]sandbox.DeploymentPolicy, len(adapters))
	for kind, a := range adapters {
		policies[kind] = a.Policy
	}
	return sandbox.PythonDeploymentContract(policies)
}
