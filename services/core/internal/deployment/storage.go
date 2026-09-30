package deployment

import (
	"context"
	"encoding/json"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/adminaudit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/coremetrics"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment/placement"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

// Storage is the persistence node management writes through, on pooled
// connections. It grants no execution authority.
type Storage interface {
	// WithNodes runs apply in one transaction that begins by locking the
	// deployment, so node changes serialize with deployment changes, pin
	// promotion and placement. It commits only when apply returns nil.
	WithNodes(ctx context.Context, apply func(NodeTx) error) error
	// ConnectNode records a node connection for the owner epoch, when the node
	// belongs to the installation and no newer epoch connected it. It reports
	// whether it did.
	ConnectNode(ctx context.Context, nodeID, connectionID string, epoch uint64) (bool, error)
	// DisconnectNode clears the node's connection when it is still this one. It
	// waits for the node row first, so an uncertain connect cannot outlive it.
	DisconnectNode(ctx context.Context, nodeID, connectionID string, epoch uint64) error
	// SampleHostHistory copies each fresh authenticated heartbeat into the
	// host history, at the Runtime sampler cadence, and returns the number of
	// samples. Neither reads nor offline nodes fill gaps.
	SampleHostHistory(ctx context.Context) (int64, error)
	// WithActivity runs apply in one transaction that locks the Session of
	// the tenant's Environment and then prunes the Session's journal. It
	// commits only when both succeed. A missing Environment, or one of a
	// deleted Session, is sessions.ErrNotFound.
	WithActivity(ctx context.Context, key AllocationKey, apply func(sessions.LockedSession, ActivityTx) error) error
}

// ActivityTx is one Session-locked activity change.
type ActivityTx interface {
	// TouchActivity records live-compute activity of the Environment.
	TouchActivity() error
}

// ExecutionStorage is the persistence deployment changes write through. Only
// the execution owner holds it: every transaction runs on the connection that
// holds the execution lease.
type ExecutionStorage interface {
	// WithDeployment runs apply in one leased transaction that begins by
	// locking the deployment. It commits only when apply returns nil.
	WithDeployment(ctx context.Context, apply func(DeploymentTx) error) error
	// WithReservation runs apply in one leased transaction that locks the
	// Session of the tenant's Environment and then prunes the Session's
	// journal. It commits only when both succeed. A missing Environment, or
	// one of a deleted Session, is sessions.ErrNotFound.
	WithReservation(ctx context.Context, key AllocationKey, apply func(sessions.LockedSession, ReservationTx) error) error
	// WithAllocation runs apply in one leased transaction that locks the
	// Session owning the Environment's allocation, deleted or not, and then
	// prunes the Session's journal. It commits only when both succeed. A
	// missing allocation is ErrNotFound.
	WithAllocation(ctx context.Context, key AllocationKey, apply func(AllocationTx) error) error
	AllocationCleanupStorage
	// ClearWake clears the allocation's wake request unless activity newer
	// than observed arrived. It runs on the lease.
	ClearWake(ctx context.Context, allocationID string, observed time.Time) error
}

// AllocationCleanupStorage runs allocation cleanup, which settles the
// owning Session's work and Environment with the allocation.
type AllocationCleanupStorage interface {
	// WithAllocationCleanup runs apply as WithAllocation does, with the
	// Session's cancellation and Environment termination bound to the same
	// transaction.
	WithAllocationCleanup(ctx context.Context, key AllocationKey, apply func(AllocationCleanupTx) error) error
}

// ReservationTx is one Session-locked allocation reservation. The Session is
// the owner of the Environment the transaction was opened for.
type ReservationTx interface {
	sessions.EnvironmentDeviceTx
	// LoadEnvironment reads the Session's Environment.
	LoadEnvironment(ctx context.Context) (sessions.Environment, error)
	// FindAllocation returns the Environment's allocation and whether it has
	// one.
	FindAllocation() (Allocation, bool, error)
	// LockDeployment locks the deployment and returns it as placement reads it.
	LockDeployment() (placement.Deployment, error)
	// LoadReserved returns the node placement the Session reserved for the
	// Environment.
	LoadReserved() (placement.Reserved, error)
	// InsertAllocation stores the allocation of the Session's Environment.
	InsertAllocation(allocation NewAllocation) (Allocation, error)
}

// AllocationTx is one Session-locked allocation change. Each write applies to
// current, the allocation LoadAllocation returned, and returns it changed. A
// write whose stored guard no longer holds is ErrAllocationConflict.
type AllocationTx interface {
	// LoadAllocation returns the Environment's allocation.
	LoadAllocation() (Allocation, error)
	// LoadSessionDevice returns the device the Session is bound to and
	// whether it is bound to one.
	LoadSessionDevice() (SessionDevice, bool, error)
	// LoadActivity returns the allocation's activity.
	LoadActivity(current Allocation) (Activity, error)
	// LoadRestore locks the deployment and returns what restoring the
	// allocation's suspended compute on its node reads.
	LoadRestore(current Allocation) (placement.Restore, error)
	// ObserveRunning records the allocation running with its creation settled.
	ObserveRunning(current Allocation) (Allocation, error)
	// Keep renews the allocation's lease.
	Keep(current Allocation) (Allocation, error)
	// SettleCreation records that the original Create can no longer change
	// resources.
	SettleCreation(current Allocation) (Allocation, error)
	// Release releases the allocation and its node placement.
	Release(current Allocation) (Allocation, error)
	// SetCompute commits the compute phase and advances its revision.
	SetCompute(current Allocation, change ComputeChange) (Allocation, error)
	// RecordObservation records the diagnostic for current's compute
	// revision and state. It changes nothing once either moved on.
	RecordObservation(current Allocation, diagnostic string) error
}

// AllocationCleanupTx is one Session-locked allocation cleanup.
type AllocationCleanupTx interface {
	AllocationTx
	sessions.EnvironmentTerminationTx
	// RevokeDevice revokes the allocation's device.
	RevokeDevice(current Allocation) error
	// RequestCleanup records that the allocation's resources await cleanup.
	RequestCleanup(current Allocation) (Allocation, error)
}

// Reader answers deployment and node queries.
type Reader interface {
	// Deployment returns the stored deployment.
	Deployment(ctx context.Context) (Record, error)
	// Snapshot returns the deployment with the counts and projections its View
	// reports, read in one statement.
	Snapshot(ctx context.Context) (Snapshot, error)
	// OwnerEpoch returns the current execution owner epoch, which fences node
	// connections.
	OwnerEpoch(ctx context.Context) (uint64, error)
	// Allocation returns the allocation of the Environment the reference names
	// with the deployment, read in one snapshot. It returns ErrInvalidInput for
	// malformed identifiers and ErrNotFound for a missing allocation.
	Allocation(ctx context.Context, ref sandbox.Reference) (AllocationRecord, error)
	// Generations returns up to 32 retained generations after the given one,
	// in order, without their credential.
	Generations(ctx context.Context, after int64) ([]GenerationRecord, error)
	// Nodes returns the installation's nodes.
	Nodes(ctx context.Context) ([]NodeRecord, error)
	// NodeHistory returns one node and its host history samples in the window,
	// one per bucket that has samples. It returns ErrInvalidInput for a
	// malformed ID and ErrNotFound for a missing node.
	NodeHistory(ctx context.Context, nodeID string, window coremetrics.Range) (NodeRecord, []HostHistoryPoint, error)
	// ReadNodes runs apply in one read-only snapshot.
	ReadNodes(ctx context.Context, apply func(NodeReads) error) error
	// ResetSessions returns up to 32 hosted Sessions after the given Session
	// ID, in ID order, that still hold or await deployment resources. Without
	// force it skips Sessions with a running Turn or a pending file write. An
	// empty after starts at the beginning; a malformed one is ErrInvalidInput.
	ResetSessions(ctx context.Context, after string, force bool) ([]ResetSession, error)
	// AddressBindings counts what is bound to an installation address, read
	// in one snapshot. publicURL is the address nodes are compared against.
	AddressBindings(ctx context.Context, publicURL string) (AddressBindings, error)
	// EnvironmentAllocation returns the Environment's allocation, including
	// for a deleted Session. It returns ErrInvalidInput for a malformed
	// identifier and ErrNotFound for a missing allocation.
	EnvironmentAllocation(ctx context.Context, key AllocationKey) (Allocation, error)
	// CredentialAllocations returns up to 32 unreleased allocations after the
	// given allocation ID, in ID order. An empty after starts at the
	// beginning.
	CredentialAllocations(ctx context.Context, after string) ([]Allocation, error)
	// ObservationSessions returns up to limit undeleted hosted Sessions after
	// the given Session ID, in ID order, whose allocation, if any, is
	// unreleased. limit is 1 to 100. It reads only and renews nothing.
	ObservationSessions(ctx context.Context, after string, limit int) (ObservationSessionPage, error)
	// NodeAllocations returns up to 1000 unreleased allocations of the node,
	// oldest first. A missing node is ErrNotFound.
	NodeAllocations(ctx context.Context, nodeID string) ([]NodeAllocation, error)
	// NodeOnline reports whether the node is connected; a missing node is
	// offline.
	NodeOnline(ctx context.Context, nodeID string) (bool, error)
	// LifecycleNodes returns the node of each allocation lifecycle, offline
	// ones included, with the empty ID for the lifecycle without a node.
	LifecycleNodes(ctx context.Context) ([]string, error)
	// LifecycleAllocations returns up to 32 allocations of the node's
	// lifecycle after the given allocation ID, in ID order. An empty nodeID
	// names the lifecycle without a node.
	LifecycleAllocations(ctx context.Context, nodeID, after string) ([]Allocation, error)
	// UnallocatedEnvironments returns up to 32 pending hosted Environments of
	// the node's lifecycle without an allocation, after the given Environment
	// ID, in ID order. It reads the committed placement and never selects a
	// replacement node.
	UnallocatedEnvironments(ctx context.Context, nodeID, after string) ([]UnallocatedEnvironment, error)
	// LifecyclePlacement returns what routing the Environment to its
	// lifecycle reads, including for a deleted Session. A missing Environment
	// is sessions.ErrNotFound.
	LifecyclePlacement(ctx context.Context, key AllocationKey) (LifecyclePlacement, error)
	// Activity returns the allocation's activity; a missing allocation is
	// ErrNotFound.
	Activity(ctx context.Context, allocationID string) (Activity, error)
	// CountComputeReservations counts the installation's allocations that
	// reserve active compute.
	CountComputeReservations(ctx context.Context, installationID string) (int64, error)
	// CountRetainedAllocations counts the installation's allocations that
	// retain resources.
	CountRetainedAllocations(ctx context.Context, installationID string) (int64, error)
}

// NodeReads loads node authentication facts.
type NodeReads interface {
	// LoadDeployment returns the deployment, locked when the transaction locks it.
	LoadDeployment() (Record, error)
	// LoadNode returns ErrInvalidInput for a malformed ID and ErrNotFound for
	// a missing or removed node.
	LoadNode(id string) (StoredNode, error)
	// LoadEnrollment returns ErrNotFound for an unknown token digest.
	LoadEnrollment(tokenDigest string) (EnrollmentRecord, error)
	// LoadGenerationSpecification returns ErrNotFound for a collected
	// generation.
	LoadGenerationSpecification(generation uint64) (GenerationSpecification, error)
	// GenerationKept reports whether the node may keep the generation: its
	// target, its serving pin or one with unreleased ownership on it.
	GenerationKept(nodeID string, generation uint64) (bool, error)
}

// NodeTx is one node management transaction.
type NodeTx interface {
	NodeReads
	ListNodes() ([]NodeRecord, error)
	// InsertNode returns ErrNodeExists when the ID is in use, including by a
	// removed node, and ErrInvalidInput for a malformed ID.
	InsertNode(node NewNode) (StoredNode, error)
	UpdateNode(id string, limits NodeLimits) error
	RemoveNode(id string) error
	// CreateEnrollment stores a token that expires in ten minutes and returns
	// its expiry.
	CreateEnrollment(enrollment NewEnrollment) (time.Time, error)
	// ConsumeEnrollment marks an unexpired, unconsumed token of the current
	// installation used by the node and reports whether it did.
	ConsumeEnrollment(tokenDigest, nodeID string) (bool, error)
	// HeartbeatNode records the node's health for its current connection and
	// reports whether the connection is still current.
	HeartbeatNode(heartbeat Heartbeat) (bool, error)
	DeleteGenerationStatus(nodeID string, generation uint64) error
	UpsertGenerationStatus(status GenerationStatusRecord) error
	PromoteServingGeneration(nodeID string, generation uint64) error
	RefreshServingReadiness(nodeID string, protocol int) error
}

// DeploymentTx is one leased deployment change.
type DeploymentTx interface {
	LoadDeployment() (Record, error)
	LoadSnapshot() (Snapshot, error)
	CountResources() (Resources, error)
	// ClaimInstallation reserves the installation for Web setup and fences the
	// previous owner epoch's node presence.
	ClaimInstallation(installationID string) error
	SetProcessDeployment(installationID, backendFingerprint string, admissionPaused bool) error
	// SetManagerDeployment records the node provider and the local node, which
	// is empty when there is none.
	SetManagerDeployment(provider, localNodeID string) error
	LoadNode(id string) (StoredNode, error)
	InsertNode(node NewNode) (StoredNode, error)
	UpdateNode(id string, limits NodeLimits) error
	// SaveSelection stores the next generation. It seals a secret bound to the
	// installation and generation; without a key it returns
	// credentialcrypto.ErrUnavailable.
	SaveSelection(selection SelectionRecord) error
	RecordConfigurationMetadata(metadata json.RawMessage) error
	// RetainGeneration keeps the current generation for the allocations that
	// still use it.
	RetainGeneration() error
	// CollectGenerations deletes retained generations nothing uses.
	CollectGenerations() error
	// StartReset pauses admission and records a reset in the clear mode,
	// with its request time, the auto deadline in seconds from now and the
	// administrator source that started it.
	StartReset(clear string, deadlineSeconds int32, source adminaudit.Source) error
	// ForceReset escalates a running auto reset to force.
	ForceReset() error
	// CancelReset forgets the running reset and restores admission.
	CancelReset() error
	// LoadResetSource returns the administrator source that started the
	// running reset.
	LoadResetSource() (adminaudit.Source, error)
	// CompleteReset forgets the selection and the reset, restores admission,
	// advances the generation and owner epoch by one, and retires the nodes,
	// enrollments and retained generations.
	CompleteReset() error
	// RecordAudit records an administrator mutation of the deployment by the
	// source the transaction's context carries.
	RecordAudit(action, installationID string) error
	// RecordAuditAs records an administrator mutation of the deployment by
	// the given source.
	RecordAuditAs(source adminaudit.Source, action, installationID string) error
}

// Record is the stored deployment.
type Record struct {
	// InstallationID is empty until an installation is claimed or configured.
	InstallationID     string
	WebManaged         bool
	Provider           string
	BackendFingerprint string
	Generation         uint64
	OwnerEpoch         uint64
	Mode               string
	AdmissionPaused    bool
	LocalNodeID        string
	IdleSeconds        int64
	RetentionSeconds   int64
	// Specification is the stored specification document.
	Specification json.RawMessage
	// Configuration holds the public configuration and metadata and, when a
	// credential is stored and could be opened, its secret.
	Configuration    sandbox.ConfigurationRecord
	CredentialStored bool
	// CredentialError is why the stored credential could not be opened: no key
	// (credentialcrypto.ErrUnavailable) or a ciphertext the key cannot open or
	// authenticate (an internal error).
	CredentialError error
	// Reset is nil unless a reset is in progress.
	Reset *ResetState
}

// ResetState is the durable state of a reset in progress.
type ResetState struct {
	Clear                string
	RequestedAt          time.Time
	DeadlineAt, ForcedAt *time.Time
}

// Snapshot is the deployment with the counts and projections of its View.
type Snapshot struct {
	Record    Record
	Resources Resources
	Rollout   Rollout
	// Remaining partitions the resources while a reset is in progress.
	Remaining ResetRemaining
}

// SelectionRecord is one generation of the selection to store.
type SelectionRecord struct {
	InstallationID, Provider, BackendFingerprint, Mode string
	Generation                                         uint64
	IdleSeconds, RetentionSeconds                      int64
	Specification                                      json.RawMessage
	// Configuration carries the secret in plaintext; the adapter seals it.
	Configuration sandbox.ConfigurationRecord
}

// GenerationRecord is a retained generation without its credential.
type GenerationRecord struct {
	Generation    uint64
	Provider      string
	Specification json.RawMessage
	Configuration sandbox.ConfigurationRecord
}

// GenerationSpecification is the provider and specification of a generation
// nodes may prepare.
type GenerationSpecification struct {
	Provider      string
	Specification json.RawMessage
}

// AllocationRecord is an allocation with the deployment that owns it.
type AllocationRecord struct {
	ID string
	// Generation is zero when the allocation has none.
	Generation     uint64
	Released       bool
	InstallationID string
	Deployment     Record
	// Retained is the allocation's generation when it is not the deployment's
	// current one and is still retained.
	Retained *GenerationRecord
}

// StoredNode is one node as stored.
type StoredNode struct {
	ID, InstallationID, Name, BackendFingerprint string
	CredentialDigest                             string
	MaxActive, MaxRetained                       int
	ConnectionID                                 string
	ConnectedEpoch                               uint64
	SpecificationDigest                          string
	DeploymentGeneration                         uint64
	ReadyGeneration                              *uint64
}

// NodeRecord is a node of the installation with its presence and usage.
type NodeRecord struct {
	ID, Name, Provider, CoreURL string
	EnrollmentID                *string
	CreatedAt                   time.Time
	LastSeenAt                  *time.Time
	Health                      NodeHealth
	Online, ServingReady        bool
	ProtocolVersion             int
	DeploymentGeneration        uint64
	TargetGeneration            uint64
	ReadyGeneration             *uint64
	TargetState                 string
	TargetDiagnostic            string
	MaxActive, MaxRetained      int
	Active, Retained, Reserved  int64
	CleanupPending              int64
	Running, Snapshots          int64
}

// NewNode is a node to store.
type NewNode struct {
	ID, InstallationID, Name, BackendFingerprint string
	CredentialDigest                             string
	MaxActive, MaxRetained                       int
	SpecificationDigest                          string
	DeploymentGeneration                         uint64
	CoreURL                                      string
	// EnrollmentID is empty for a node that no enrollment registered.
	EnrollmentID string
}

// NodeLimits is a node's name and capacity.
type NodeLimits struct {
	Name                   string
	MaxActive, MaxRetained int
}

// EnrollmentRecord is a stored enrollment token.
type EnrollmentRecord struct {
	ID, InstallationID     string
	ExpiresAt              time.Time
	Consumed               bool
	MaxActive, MaxRetained int
}

// NewEnrollment is an enrollment token to store, by digest.
type NewEnrollment struct {
	ID, TokenDigest, InstallationID string
	MaxActive, MaxRetained          int
}

// Heartbeat is a node's health for one connection.
type Heartbeat struct {
	NodeID, ConnectionID string
	Epoch                uint64
	Health               NodeHealth
}

// GenerationStatusRecord is a node's preparation state of one generation.
type GenerationStatusRecord struct {
	NodeID, ConnectionID string
	Generation           uint64
	SpecificationDigest  string
	OwnerEpoch           uint64
	State, Diagnostic    string
}
