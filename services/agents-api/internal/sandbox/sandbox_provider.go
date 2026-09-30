// Package sandbox defines the Sandbox Provider contract for authorized compute.
// Start at sandbox_provider.go and docs/sandbox-provider.md when adding an adapter.
// Core owns durable Environment/allocation state and lifecycle serialization;
// SandboxProvider owns compute and bootstrap, Runtime owns capability preparation,
// and Harness adapters own native execution. Compute running is not execution ready.
//
// Required operations are on SandboxProvider. CheckpointProvider and runtimeobs
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
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/providercontract"
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

// CheckpointProvider is an explicitly declared extension. It supplies
// exact-incarnation operations; Worker and Store remain the lifecycle owner.
type CheckpointProvider interface {
	SandboxProvider
	Initial(context.Context, Reference) (Compute, error)
	NewCompute(context.Context, Reference, uint64, *SnapshotIdentity) (Compute, error)
	GetCompute(context.Context, Reference, Compute) (ComputeState, error)
	Suspend(context.Context, SuspendRequest) (ComputeState, error)
	Resume(context.Context, ResumeRequest) (ComputeState, error)
	KillCompute(context.Context, Reference, Compute) error
	DeleteSnapshot(context.Context, Reference, SnapshotIdentity) error
	RunCommandCompute(context.Context, Reference, Compute, Command) (CommandResult, error)
	// ResumeCompute thaws only the same resident instance after an aborted pause.
	ResumeCompute(context.Context, Reference, Compute) (ComputeState, error)
}
