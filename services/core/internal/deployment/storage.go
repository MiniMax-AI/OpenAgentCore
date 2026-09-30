package deployment

import (
	"context"
	"encoding/json"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/coremetrics"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
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
}

// ExecutionStorage is the persistence deployment changes write through. Only
// the execution owner holds it: every transaction runs on the connection that
// holds the execution lease.
type ExecutionStorage interface {
	// WithDeployment runs apply in one leased transaction that begins by
	// locking the deployment. It commits only when apply returns nil.
	WithDeployment(ctx context.Context, apply func(DeploymentTx) error) error
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
	// RecordAudit records an administrator mutation of the deployment.
	RecordAudit(action, installationID string) error
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
