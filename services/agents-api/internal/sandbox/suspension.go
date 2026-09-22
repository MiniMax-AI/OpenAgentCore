package sandbox

import (
	"context"
	"errors"
)

// ErrComputeUnconfirmed requires observation of the retained operation identity;
// it does not authorize another Create, capture, restore, or cold start.
var ErrComputeUnconfirmed = errors.New("sandbox lifecycle outcome unconfirmed")

// Compute identifies one incarnation of an allocation. Name is provider-derived.
// ID is empty only until the original create or restore result is observed.
type Compute struct {
	Generation   uint64
	Name         string
	ID           string
	RestoredFrom *SnapshotIdentity
}

// SnapshotIdentity is provider evidence from a verified full snapshot. Core
// persists it unchanged and records consumption separately; it never invents
// paths, checksums, native checkpoint fields, or source identity.
type SnapshotIdentity struct {
	Reference        string
	ID               string
	Digest           string
	CheckpointID     string
	CheckpointRoot   string
	OperationID      string
	SourceGeneration uint64
	SourceName       string
	SourceID         string
}

type ComputeState struct {
	Compute           Compute
	Status            string
	BootstrapComplete bool
	Snapshot          *SnapshotIdentity
	SourceStopped     bool
}
type SuspendRequest struct {
	Reference   Reference
	OperationID string
	Source      Compute
	Snapshot    *SnapshotIdentity
	// Recovery observes the previous attempt and never starts a new capture.
	ObserveOnly bool
}
type ResumeRequest struct {
	Reference   Reference
	OperationID string
	Snapshot    SnapshotIdentity
	Target      Compute
	// Recovery observes the previous target and never starts a new restore.
	ObserveOnly bool
}

// CheckpointProvider is optional. It supplements the existing provider with
// exact-incarnation operations; Worker and Store remain the lifecycle owner.
type CheckpointProvider interface {
	Provider
	Initial(Reference) Compute
	NewCompute(Reference, uint64, *SnapshotIdentity) (Compute, error)
	GetCompute(context.Context, Reference, Compute) (ComputeState, error)
	Suspend(context.Context, SuspendRequest) (ComputeState, error)
	Resume(context.Context, ResumeRequest) (ComputeState, error)
	KillCompute(context.Context, Reference, Compute) error
	DeleteSnapshot(context.Context, Reference, SnapshotIdentity) error
	RunCommandCompute(context.Context, Reference, Compute, Command) (CommandResult, error)
	// ResumeCompute thaws only the same resident instance after an aborted pause.
	ResumeCompute(context.Context, Reference, Compute) (ComputeState, error)
}
