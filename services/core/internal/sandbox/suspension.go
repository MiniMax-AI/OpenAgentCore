package sandbox

import (
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
