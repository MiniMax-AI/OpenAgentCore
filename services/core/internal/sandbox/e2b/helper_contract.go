package e2b

import (
	"slices"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/runtimebootstrap"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimeobs"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

//go:generate go run ./internal/contractgen

// This adapter-private boundary is documented in tools/e2b-provider/README.md.
const ProtocolVersion = 1
const MaxOutputBytes = 1024 * 1024
const MaxRequestBytes = 72 * 1024 * 1024
const MaxResponseBytes = 16 * 1024 * 1024
const MaxCredentialReferences = 32
const MaxObservationReferences = runtimeobs.MaxBatchTargets
const MaxCommandInputBytes = sandbox.MaxCommandInputBytes

// HelperOperations declares the complete set of one-shot helper operations.
func HelperOperations() []string {
	return []string{"create", "inspect", "renew", "kill", "command", "validate_deployment", "observe", "list_templates", "list_builds", "verify_credential"}
}

// HelperErrors are sanitized wire outcomes; an empty code denotes success.
func HelperErrors() []string {
	return []string{"", "invalid", "ownership", "exists", "not_found", "command_unconfirmed", "unconfirmed", "template_invalid", "team_mismatch", "unauthorized"}
}

// Validate checks the operation envelope before the helper can start. Native
// configuration and operation outcomes retain their existing adapter validation.
func (q Request) Validate() error {
	if q.Version != ProtocolVersion || !slices.Contains(HelperOperations(), q.Operation) || q.Deadline.IsZero() {
		return sandbox.ErrInvalid
	}
	if q.Operation != "list_templates" && q.Operation != "list_builds" && !validID(q.Config.InstallationID) {
		return sandbox.ErrInvalid
	}
	switch q.Operation {
	case "observe":
		if len(q.References) < 1 || len(q.References) > MaxObservationReferences {
			return sandbox.ErrInvalid
		}
		seen := map[sandbox.Reference]bool{}
		for _, r := range q.References {
			if !validReference(r) || seen[r] {
				return sandbox.ErrInvalid
			}
			seen[r] = true
		}
	case "verify_credential":
		if len(q.References) > MaxCredentialReferences {
			return sandbox.ErrInvalid
		}
		for _, r := range q.References {
			if !validReference(r) {
				return sandbox.ErrInvalid
			}
		}
	case "validate_deployment", "list_templates", "list_builds":
	default:
		if !validReference(q.Reference) {
			return sandbox.ErrInvalid
		}
	}
	return nil
}

type Request struct {
	Version   int
	Operation string
	Config    Config
	Reference sandbox.Reference
	// References lists the allocations of one read-only observe request.
	References       []sandbox.Reference          `json:",omitempty"`
	Bootstrap        *sandbox.Bootstrap           `json:",omitempty"`
	RuntimeBootstrap *runtimebootstrap.Connection `json:",omitempty"`
	Command          *sandbox.Command             `json:",omitempty"`
	Deadline         time.Time
}
type Response struct {
	Version         int
	Info            *sandbox.Info          `json:",omitempty"`
	Command         *sandbox.CommandResult `json:",omitempty"`
	ErrorCode       string
	DeploymentValid bool              `json:",omitempty"`
	TemplateBuild   *TemplateBuild    `json:",omitempty"`
	Templates       []TemplateSummary `json:",omitempty"`
	Builds          []ReadyBuild      `json:",omitempty"`
	Observations    []Observation     `json:",omitempty"`
}

func (r Response) Validate() error {
	if r.Version != ProtocolVersion || !slices.Contains(HelperErrors(), r.ErrorCode) {
		return sandbox.ErrInvalid
	}
	return nil
}

type TemplateSummary struct {
	ID    string   `json:"id"`
	Names []string `json:"names"`
}
type ReadyBuild struct {
	ID        string `json:"id"`
	CPUs      uint32 `json:"cpus"`
	MemoryMiB uint32 `json:"memory_mib"`
}
