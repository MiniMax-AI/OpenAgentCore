// Package microsandbox is the pure-Go client for the colocated SDK helper.
// Core owns lifecycle intent; the helper owns no database or background service.
package microsandbox

import (
	"context"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/workspacefs"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

const ProtocolVersion = 5
const SDKVersion = "v0.7.2"
const MaxOutputBytes = 1024 * 1024
const MaxRequestBytes = 72 * 1024 * 1024
const MaxResponseBytes = 16 * 1024 * 1024

// Config is trusted deployment configuration. Paths and hashes refer to one
// immutable, qualified installation. Network is explicitly used on create and restore.
type Config struct {
	// ExternalWorkspace is derived from the immutable deployment specification.
	ExternalWorkspace  bool
	InstallationID     string
	HelperPath         string
	RuntimeHome        string
	RuntimePath        string
	FirmwarePath       string
	RuntimeSHA256      string
	FirmwareSHA256     string
	Image              string
	MemoryMiB          uint32
	CPUs               uint8
	RootDiskMiB        uint32
	EnvironmentDiskMiB uint32
	Network            NetworkPolicy
}

type NetworkPolicy struct {
	DefaultEgress  string
	DefaultIngress string
	Rules          []NetworkRule
}
type NetworkRule struct{ Action, Direction, Destination, Protocol, Port string }

// Native snapshot wire types belong to this adapter.
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

type State struct {
	Compute           Compute
	Status            string
	BootstrapComplete bool
	Snapshot          *SnapshotIdentity
	SourceStopped     bool
}
type SuspendRequest struct {
	Reference   sandbox.Reference
	OperationID string
	Source      Compute
	Snapshot    *SnapshotIdentity
	// Recovery observes the previous attempt and never starts a new capture.
	ObserveOnly bool
}
type ResumeRequest struct {
	Workspace   *workspacefs.Binding `json:",omitempty"`
	Reference   sandbox.Reference
	OperationID string
	Snapshot    SnapshotIdentity
	Target      Compute
	// Recovery observes the previous target and never starts a new restore.
	ObserveOnly bool
}

// Request and Response are the finite, private helper boundary. Confidential
// Bootstrap and Command bytes travel only through stdin and are never logged.
// WorkspaceDirectory is the resolved private helper input, without filesystem adapter configuration.
type WorkspaceDirectory struct {
	Path     string
	ObjectID string
}

type Request struct {
	Workspace *WorkspaceDirectory `json:",omitempty"`
	Version   int
	Operation string
	Config    Config
	Reference sandbox.Reference
	Compute   Compute
	Bootstrap *sandbox.Bootstrap
	Command   *sandbox.Command
	Suspend   *SuspendRequest
	Resume    *ResumeRequest
	Snapshot  *SnapshotIdentity
	Deadline  time.Time
}
type Response struct {
	// CreateSettled binds State to a completed Create rejection or an initial_info
	// absence whose allocation lock permanently prevents a later initial Create.
	CreateSettled bool `json:",omitempty"`
	Version       int
	State         *State
	Command       *sandbox.CommandResult
	Metrics       *Metrics
	ErrorCode     string
}

// Metrics is the bounded provider-helper projection used by Core observability.
// ObservedAt and Uptime preserve the same native registry sample at millisecond
// precision. It excludes instantaneous CPU percent and provider-native names.
type Metrics struct {
	ObservedAt       time.Time
	Uptime           time.Duration
	VCPUTimeNs       uint64
	MemoryBytes      uint64
	MemoryLimitBytes uint64
}

type Caller interface {
	Call(context.Context, Request) (Response, error)
}
