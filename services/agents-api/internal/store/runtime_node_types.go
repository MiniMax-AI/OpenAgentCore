package store

import (
	"errors"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
	"time"
)

var (
	ErrRuntimeNodeUnavailable     = errors.New("sandbox node unavailable")
	ErrRuntimeNodeInUse           = errors.New("sandbox node retains resources")
	ErrRuntimeNodeCredential      = errors.New("invalid sandbox node credential")
	ErrRuntimeLocalNodeConfigured = errors.New("local sandbox node is enabled in deployment configuration")
)

type RuntimeNodeIdentity struct {
	SpecificationDigest  string `json:"specification_digest"`
	DeploymentGeneration uint64 `json:"deployment_generation"`
	NodeID               string `json:"node_id"`
	InstallationID       string `json:"installation_id"`
	Provider             string `json:"provider"`
	BackendFingerprint   string `json:"-"`
	MaxActive            int    `json:"max_active"`
	MaxRetained          int    `json:"max_retained"`
}
type RuntimeNodeCapacity struct {
	MaxActive   int `json:"max_active"`
	MaxRetained int `json:"max_retained"`
}

type RuntimeNodeEnrollment struct {
	SpecificationDigest  string `json:"specification_digest"`
	DeploymentGeneration uint64 `json:"deployment_generation"`
	NodeID               string `json:"node_id"`
	Credential           string `json:"credential"`
	Name                 string `json:"name"`
	Provider             string `json:"provider"`
	BackendFingerprint   string `json:"backend_fingerprint"`
}
type RuntimeNodeHealth struct {
	Host *RuntimeNodeHost `json:"-"`
	// Fixed reason for the last reported unreadiness; absent while the provider is ready. Clients treat an unknown value as provider_unavailable.
	Diagnostic           string `json:"diagnostic,omitempty" enums:"provider_unavailable,docker_unavailable,docker_limits_unsupported,runtime_image_unavailable,kvm_unavailable,microsandbox_artifacts_unavailable,capacity_insufficient"`
	ProviderReady        bool   `json:"provider_ready"`
	CPUCount             *int64 `json:"cpu_count"`
	AvailableMemoryBytes *int64 `json:"available_memory_bytes"`
	AvailableDiskBytes   *int64 `json:"available_disk_bytes"`
}
type RuntimeNode struct {
	RuntimeNodeHealth
	Running        int64      `json:"running"`
	Snapshots      int64      `json:"snapshots"`
	ID             string     `json:"id"`
	Name           string     `json:"name"`
	Provider       string     `json:"provider"`
	Online         bool       `json:"online"`
	LastSeenAt     *time.Time `json:"last_seen_at"`
	MaxActive      int        `json:"max_active"`
	MaxRetained    int        `json:"max_retained"`
	Active         int64      `json:"active"`
	Reserved       int64      `json:"reserved"`
	Retained       int64      `json:"retained"`
	CleanupPending int64      `json:"cleanup_pending"`
	CreatedAt      time.Time  `json:"created_at"`
}
type RuntimeNodeUpdate struct {
	Name        string `json:"name"`
	MaxActive   int    `json:"max_active"`
	MaxRetained int    `json:"max_retained"`
}
type RuntimePlacement struct {
	Diagnostic   string `json:"diagnostic"`
	NodeID       string `json:"node_id"`
	NodeName     string `json:"node_name"`
	Available    bool   `json:"available"`
	State        string `json:"state"`
	ComputePhase string `json:"compute_phase"`
}
type SandboxDeploymentResources struct {
	Allocations int64 `json:"allocations"`
	Pending     int64 `json:"pending"`
}
type SandboxE2BView struct {
	Template             string `json:"template"`
	CredentialConfigured bool   `json:"credential_configured"`
}
type RuntimeDeploymentView struct {
	Specification       *sandbox.DeploymentSpec    `json:"specification,omitempty"`
	SpecificationDigest string                     `json:"specification_digest,omitempty"`
	Generation          uint64                     `json:"generation"`
	Mode                string                     `json:"mode"`
	Resources           SandboxDeploymentResources `json:"resources"`
	E2B                 *SandboxE2BView            `json:"e2b,omitempty"`
	CoreURL             string                     `json:"core_url"`
	InstallationID      string                     `json:"installation_id"`
	Provider            string                     `json:"provider"`
	Maintenance         bool                       `json:"maintenance"`
	OwnerEpoch          uint64                     `json:"owner_epoch"`
}
type RuntimeNodeAllocation struct {
	Diagnostic     string    `json:"diagnostic"`
	ID             string    `json:"id"`
	NodeID         string    `json:"node_id"`
	TenantID       string    `json:"tenant_id"`
	SessionID      string    `json:"session_id"`
	EnvironmentID  string    `json:"environment_id"`
	State          string    `json:"state"`
	ComputePhase   string    `json:"compute_phase"`
	Initialization string    `json:"initialization"`
	CreatedAt      time.Time `json:"created_at"`
}
