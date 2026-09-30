// Package sandbox defines the Sandbox Provider contract for authorized compute.
// Start at sandbox_provider.go and docs/sandbox-provider.md when adding an adapter.
// Core owns durable Environment/allocation state and lifecycle serialization;
// SandboxProvider owns compute and bootstrap, Runtime owns capability preparation,
// and Harness adapters own native execution. Compute running is not execution ready.
//
// Required operations are on SandboxProvider. SuspensionProvider and runtimeobs
// observation remain separate small interfaces. Every registered adapter explicitly
// declares and implements each operation, including safe Unsupported rejections.
// Method-set presence never means an extension is supported. ValidateProvider and
// the common contract tests check declaration completeness and implementation.
//
// Registration is explicit construction, not a global init-time registry. Node-local
// adapters register in sandbox/providers; Core's managed setup
// constructs direct adapters or node proxies. execution.RuntimeProvider binds the
// selected adapter to installation, backend, deployment generation and node identity.
// Keep vendor configuration at those construction boundaries; common lifecycle code
// selects behavior through these contracts, never through a vendor name.
package sandbox

import (
	"context"
	"errors"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/providercontract"
)

var (
	ErrInvalid            = errors.New("invalid sandbox configuration")
	ErrOwnership          = errors.New("sandbox ownership mismatch")
	ErrExists             = errors.New("sandbox allocation already exists")
	ErrNotFound           = errors.New("sandbox allocation not found")
	ErrCommandUnconfirmed = errors.New("initialization command outcome unconfirmed; reclaim allocation before reuse")
)

// Reference must be persisted by the caller before Create. AllocationID is a fresh
// UUID for one attempt, not the Environment ID. Serialize lifecycle operations for
// an allocation; a lost Create response is resolved with GetInfo, never by replay.
type Reference struct{ TenantID, EnvironmentID, AllocationID string }

type Bootstrap struct {
	Reference
	SessionID, DeviceID, CoreURL, Credential string
	NetworkAccess                            string
	AllowedDomains                           []string
}

// Info describes compute only. Running does not establish daemon authentication,
// native preparation, Environment readiness or a renewable provider lease.
type Info struct {
	Reference
	ProviderID, State string
	// BootstrapComplete is provider evidence that initialization has reached its
	// last mutating step. It does not establish daemon or native readiness.
	BootstrapComplete bool
	// CreateSettled proves that the original create and initialization attempt can
	// no longer mutate resources. An absent observation needs this explicit proof;
	// an ordinary missing resource or empty provider listing is not sufficient.
	CreateSettled bool
}
type Command struct {
	Args      []string
	Directory string
	// Stdin carries confidential initialization bytes without exposing them in argv.
	Stdin []byte
}

const MaxCommandInputBytes = 50*1024*1024 + 32

type CommandResult struct {
	Stdout, Stderr string
	ExitCode       int
}

// SandboxProvider manages one persisted Reference at a time. Every call has a
// bounded context; cancellation ends the caller's wait, not proof of native stop.
// Non-nil errors retain ownership, including partial results. Do not retry Create
// or RunCommand after unknown delivery; observe/reclaim the original Reference.
// Implementations verify installation plus Reference ownership before mutation.
// See docs/sandbox-provider.md for settlement, cleanup and retry requirements.
type SandboxProvider interface {
	providercontract.Declared
	// Create performs the original attempt once; no credential overwrite on conflict.
	Create(context.Context, Bootstrap) (Info, error)
	// GetInfo observes compute without creating, starting, renewing or preparing it.
	GetInfo(context.Context, Reference) (Info, error)
	// Renew extends an existing native lease where supported; otherwise it observes.
	// It never revives stopped compute or establishes execution readiness.
	Renew(context.Context, Reference) (Info, error)
	// Kill confirms removal of owned compute and retained resources. Absence is
	// idempotent, but nil alone cannot settle an outstanding Create.
	Kill(context.Context, Reference) error
	RunCommand(context.Context, Reference, Command) (CommandResult, error)
}

// SuspensionProvider is an explicitly declared extension. It supplies
// exact-incarnation operations; Worker and Store remain the lifecycle owner.
type SuspensionProvider interface {
	SandboxProvider
	Initial(context.Context, Reference) (Compute, error)
	NewCompute(context.Context, Reference, uint64, *RetainedState) (Compute, error)
	GetCompute(context.Context, Reference, Compute) (ComputeState, error)
	RenewCompute(context.Context, Reference, Compute) (ComputeState, error)
	Suspend(context.Context, SuspendRequest) (ComputeState, error)
	Resume(context.Context, ResumeRequest) (ComputeState, error)
	KillCompute(context.Context, Reference, Compute) error
	DeleteRetained(context.Context, Reference, RetainedState) error
	RunCommandCompute(context.Context, Reference, Compute, Command) (CommandResult, error)
	// ResumeCompute thaws only the same resident instance after an aborted pause.
	ResumeCompute(context.Context, Reference, Compute) (ComputeState, error)
}

// ProcessPaths locates installed adapter helpers and their private state. The
// launcher supplies roots from the distribution layout; adapters own subpaths.
type ProcessPaths struct {
	ArtifactRoot string
	StateRoot    string
}

// ValidateRetained checks the shared envelope; only its adapter interprets Data.
func ValidateRetained(s RetainedState) error {
	if s.Reference == "" || s.ID == "" || s.OperationID == "" || s.SourceID == "" || s.SourceName == "" || len(s.Data) == 0 || len(s.Data) > 64*1024 {
		return ErrInvalid
	}
	return nil
}

// ErrComputeUnconfirmed requires observation of the retained operation identity;
// it does not authorize another Create, capture, restore, or cold start.
var ErrComputeUnconfirmed = errors.New("sandbox lifecycle outcome unconfirmed")

// Compute identifies one incarnation of an allocation. Name is provider-derived.
// ID is empty only until the original create or restore result is observed.
type Compute struct {
	Generation   uint64
	Name         string
	ID           string
	RestoredFrom *RetainedState
}

// RetainedState is adapter-owned recoverable state. Data is opaque to Core.
// A retained state does not imply an independent snapshot.
type RetainedState struct {
	Reference        string
	ID               string
	Data             string
	OperationID      string
	SourceGeneration uint64
	SourceName       string
	SourceID         string
}

type ComputeState struct {
	Compute           Compute
	Status            string
	BootstrapComplete bool
	Retained          *RetainedState
	ResourcesReleased bool
	SuspendSettled    bool
}
type SuspendRequest struct {
	Reference   Reference
	OperationID string
	Source      Compute
	Retained    *RetainedState
	// Recovery settles the previous attempt without another capture.
	// Ownership-verified cleanup of a durable retained artifact may complete.
	ReconcileOnly bool
}
type ResumeRequest struct {
	Reference   Reference
	OperationID string
	Retained    RetainedState
	Target      Compute
	// Recovery observes the previous target and never starts a new restore.
	ReconcileOnly bool
}

// ValidateComputeResult binds an observation to its precommitted incarnation.
func ValidateComputeResult(want, got Compute) error {
	if got.ID == "" || got.Name != want.Name || got.Generation != want.Generation || (want.ID != "" && got.ID != want.ID) || (want.RestoredFrom == nil) != (got.RestoredFrom == nil) {
		return ErrOwnership
	}
	if want.RestoredFrom != nil && *want.RestoredFrom != *got.RestoredFrom {
		return ErrOwnership
	}
	return nil
}

// ValidateSuspendResult distinguishes settled rollback from uncertain native work.
func ValidateSuspendResult(q SuspendRequest, s ComputeState) error {
	if ValidateComputeResult(q.Source, s.Compute) != nil {
		return ErrOwnership
	}
	if !s.SuspendSettled || !s.BootstrapComplete {
		return ErrComputeUnconfirmed
	}
	if s.Retained == nil {
		if !q.ReconcileOnly || s.ResourcesReleased || (s.Status != "running" && s.Status != "paused") {
			return ErrComputeUnconfirmed
		}
		return nil
	}
	v := s.Retained
	if ValidateRetained(*v) != nil || v.OperationID != q.OperationID || v.SourceID != q.Source.ID || v.SourceName != q.Source.Name || v.SourceGeneration != q.Source.Generation || (q.Retained != nil && *q.Retained != *v) {
		return ErrOwnership
	}
	if !s.ResourcesReleased || s.Status != "suspended" {
		return ErrComputeUnconfirmed
	}
	return nil
}

// SuspensionStateVersion fences incompatible durable lifecycle shapes.
const SuspensionStateVersion = "1"
