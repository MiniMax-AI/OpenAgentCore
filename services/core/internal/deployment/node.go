package deployment

import (
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

type NodeIdentity struct {
	SpecificationDigest  string `json:"specification_digest"`
	DeploymentGeneration uint64 `json:"deployment_generation"`
	NodeID               string `json:"node_id"`
	InstallationID       string `json:"installation_id"`
	Provider             string `json:"provider"`
	BackendFingerprint   string `json:"-"`
	MaxActive            int    `json:"max_active"`
	MaxRetained          int    `json:"max_retained"`
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
	MaxActive   int `json:"max_active"`
	MaxRetained int `json:"max_retained"`
}

type Enrollment struct {
	SpecificationDigest  string `json:"specification_digest"`
	DeploymentGeneration uint64 `json:"deployment_generation"`
	NodeID               string `json:"node_id"`
	Credential           string `json:"credential"`
	Name                 string `json:"name"`
	Provider             string `json:"provider"`
	BackendFingerprint   string `json:"backend_fingerprint"`
	// The Core origin this node stores and connects to, such as https://core.example. It must equal the installation public URL; otherwise enrollment gets 409 sandbox_node_address_mismatch and the token stays unused.
	CoreURL string `json:"core_url"`
}

type NodeHealth struct {
	Host *NodeHost `json:"-"`
	// Fixed reason for the last reported unreadiness; absent while the provider is ready. Clients treat an unknown value as provider_unavailable.
	Diagnostic           string `json:"diagnostic,omitempty" enums:"provider_unavailable,host_unsupported,artifacts_unavailable,runtime_download_failed,runtime_image_unavailable,capacity_insufficient"`
	ProviderReady        bool   `json:"provider_ready" binding:"required"`
	CPUCount             *int64 `json:"cpu_count" extensions:"x-nullable" binding:"required"`
	AvailableMemoryBytes *int64 `json:"available_memory_bytes" extensions:"x-nullable" binding:"required"`
	AvailableDiskBytes   *int64 `json:"available_disk_bytes" extensions:"x-nullable" binding:"required"`
}

type Node struct {
	Rollout NodeRollout `json:"rollout" binding:"required"`
	NodeHealth
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
	// The Core address this node enrolled with. A node whose address differs
	// from the installation public URL receives no new sandboxes; re-add it.
	CoreURL string `json:"core_url" binding:"required"`
	// The enrollment_id of the command that registered this node (POST /core/v1/sandbox/enrollment-tokens); null for nodes enrolled before Core recorded it.
	EnrollmentID *string `json:"enrollment_id" extensions:"x-nullable" binding:"required"`
}

type NodeUpdate struct {
	Name      string `json:"name" binding:"required"`
	MaxActive int    `json:"max_active" binding:"required"`
	// Docker never suspends, so Core replaces this with max_active; microsandbox uses both limits.
	MaxRetained int `json:"max_retained"`
}

// NodeStatus reports only the authenticated node's Core-owned presence.
type NodeStatus struct {
	NodeIdentity
	Connected     bool `json:"connected"`
	ProviderReady bool `json:"provider_ready"`
}

type NodeConfiguration struct {
	MaxActive           int                    `json:"max_active"`
	MaxRetained         int                    `json:"max_retained"`
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
