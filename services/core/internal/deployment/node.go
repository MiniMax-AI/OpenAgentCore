package deployment

import (
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/machineapi"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

// NodeIdentity is the wire identity of a node and the backend namespace Core keeps for it.
type NodeIdentity struct {
	machineapi.NodeIdentity
	BackendFingerprint string `json:"-"`
}

// EnrollmentToken is an issued one-use node enrollment token. ID is a public,
// non-secret handle that never authenticates; the node the token registers
// reports it as enrollment_id.
type EnrollmentToken struct {
	Token     string
	ExpiresAt time.Time
	ID        string
}

type Capacity struct {
	MaxActive   int `json:"max_active" binding:"required"`
	MaxRetained int `json:"max_retained" binding:"required"`
}

// EnrollmentLimits are the limits an enrollment token approves. A nil limit is unset.
type EnrollmentLimits struct {
	MaxActive, MaxRetained *int
}

type NodeHealth struct {
	Host                 *NodeHost `json:"-"`
	CPUCount             *int64    `json:"cpu_count" extensions:"x-nullable" binding:"required"`
	AvailableMemoryBytes *int64    `json:"available_memory_bytes" extensions:"x-nullable" binding:"required"`
	AvailableDiskBytes   *int64    `json:"available_disk_bytes" extensions:"x-nullable" binding:"required"`
}

type Node struct {
	Rollout NodeRollout `json:"rollout" binding:"required"`
	NodeHealth
	// True while the node is online and its serving generation is ready.
	ProviderReady  bool       `json:"provider_ready" binding:"required"`
	Running        int64      `json:"running" binding:"required"`
	Snapshots      int64      `json:"snapshots" binding:"required"`
	ID             string     `json:"id" binding:"required"`
	Name           string     `json:"name" binding:"required"`
	Provider       string     `json:"provider" binding:"required"`
	Online         bool       `json:"online" binding:"required"`
	LastSeenAt     *time.Time `json:"last_seen_at" extensions:"x-nullable" binding:"required"`
	MaxActive      int        `json:"max_active" binding:"required"`
	MaxRetained    int        `json:"max_retained" binding:"required"`
	Active         int64      `json:"active" binding:"required"`
	Reserved       int64      `json:"reserved" binding:"required"`
	Retained       int64      `json:"retained" binding:"required"`
	CleanupPending int64      `json:"cleanup_pending" binding:"required"`
	CreatedAt      time.Time  `json:"created_at" binding:"required"`
	// The limits Core recommends for the node's latest host measurements and the current specification; null before the node reports them.
	RecommendedCapacity *Capacity `json:"recommended_capacity" extensions:"x-nullable" binding:"required"`
	// The Core address this node enrolled with. A node whose address differs
	// from the installation public URL receives no new sandboxes; re-add it.
	CoreURL string `json:"core_url" binding:"required"`
	// The enrollment_id of the command that registered this node (POST /core/v1/sandbox/enrollment-tokens); null for nodes enrolled before Core recorded it.
	EnrollmentID *string `json:"enrollment_id" extensions:"x-nullable" binding:"required"`
}

type NodeUpdate struct {
	Name      string `json:"name" binding:"required"`
	MaxActive int    `json:"max_active" binding:"required"`
	// A Provider that does not checkpoint never suspends, so Core replaces this with max_active; a Provider that checkpoints uses both limits.
	MaxRetained int `json:"max_retained"`
}

// NodeConfiguration answers GET /api/v1/sandbox-node/configuration. Its specification is the Sandbox Provider protocol's type, which the machine wire file cannot import.
type NodeConfiguration struct {
	InstallationID      string                 `json:"installation_id"`
	Provider            string                 `json:"provider"`
	CoreURL             string                 `json:"core_url"`
	Generation          uint64                 `json:"generation"`
	Specification       sandbox.DeploymentSpec `json:"specification"`
	SpecificationDigest string                 `json:"specification_digest"`
}

// NodeHost is deployment telemetry, not sandbox capacity authority.
type NodeHost struct {
	EffectiveCPUCores    *float64   `json:"effective_cpu_cores" extensions:"x-nullable" binding:"required"`
	CPUUtilization       *float64   `json:"cpu_utilization" extensions:"x-nullable" binding:"required"`
	TotalMemoryBytes     *int64     `json:"total_memory_bytes" extensions:"x-nullable" binding:"required"`
	AvailableMemoryBytes *int64     `json:"available_memory_bytes" extensions:"x-nullable" binding:"required"`
	AvailableDiskBytes   *int64     `json:"available_disk_bytes" extensions:"x-nullable" binding:"required"`
	ObservedAt           *time.Time `json:"observed_at" extensions:"x-nullable" binding:"required"`
}

type HostHistoryPoint struct {
	Start                 time.Time `json:"start" binding:"required"`
	CPUUtilizationMax     *float64  `json:"cpu_utilization_max" extensions:"x-nullable" binding:"required"`
	MemoryUsedBytesMax    *int64    `json:"memory_used_bytes_max" extensions:"x-nullable" binding:"required"`
	AvailableDiskBytesMin *int64    `json:"available_disk_bytes_min" extensions:"x-nullable" binding:"required"`
}

type HostHistory struct {
	ResolutionSeconds int64              `json:"resolution_seconds" binding:"required"`
	Points            []HostHistoryPoint `json:"points" binding:"required"`
}

type NodeDetail struct {
	Node
	Host    NodeHost    `json:"host" binding:"required"`
	History HostHistory `json:"history" binding:"required"`
}
