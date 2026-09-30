package store

import (
	"errors"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"time"
)

var (
	ErrSandboxNodesPreparing      = errors.New("sandbox nodes are preparing the target generation")
	ErrRuntimeNodeUnavailable     = errors.New("sandbox node unavailable")
	ErrRuntimeNodeInUse           = errors.New("sandbox node retains resources")
	ErrRuntimeNodeCredential      = errors.New("invalid sandbox node credential")
	ErrRuntimeLocalNodeConfigured = errors.New("local sandbox node is enabled in deployment configuration")
	// ErrRuntimeNodeAddressMismatch rejects an enrollment whose Core address is not
	// the installation public URL. The token stays unconsumed.
	ErrRuntimeNodeAddressMismatch = errors.New("sandbox node Core address differs from the public URL")
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

// RuntimeNodeEnrollmentToken is an issued one-use node enrollment token. ID is a
// public, non-secret handle that never authenticates; the node the token registers
// reports it as enrollment_id.
type RuntimeNodeEnrollmentToken struct {
	Token     string
	ExpiresAt time.Time
	ID        string
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
	// The Core origin this node stores and connects to, such as https://core.example. It must equal the installation public URL; otherwise enrollment gets 409 sandbox_node_address_mismatch and the token stays unused.
	CoreURL string `json:"core_url"`
}
type RuntimeNodeHealth struct {
	Host *RuntimeNodeHost `json:"-"`
	// Fixed reason for the last reported unreadiness; absent while the provider is ready. Clients treat an unknown value as provider_unavailable.
	Diagnostic           string `json:"diagnostic,omitempty" enums:"provider_unavailable,docker_unavailable,docker_limits_unsupported,runtime_download_failed,runtime_image_unavailable,kvm_unavailable,microsandbox_artifacts_unavailable,capacity_insufficient"`
	ProviderReady        bool   `json:"provider_ready"`
	CPUCount             *int64 `json:"cpu_count"`
	AvailableMemoryBytes *int64 `json:"available_memory_bytes"`
	AvailableDiskBytes   *int64 `json:"available_disk_bytes"`
}
type RuntimeNode struct {
	Rollout SandboxNodeRollout `json:"rollout"`
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
	// The Core address this node enrolled with. A node whose address differs
	// from the installation public URL receives no new sandboxes; re-add it.
	CoreURL string `json:"core_url"`
	// The enrollment_id of the command that registered this node (POST /core/v1/sandbox/enrollment-tokens); null for nodes enrolled before Core recorded it.
	EnrollmentID *string `json:"enrollment_id" extensions:"x-nullable"`
}
type RuntimeNodeUpdate struct {
	Name      string `json:"name"`
	MaxActive int    `json:"max_active"`
	// Docker never suspends, so Core replaces this with max_active; microsandbox uses both limits.
	MaxRetained int `json:"max_retained"`
}
type SandboxDeploymentResources struct {
	Allocations int64 `json:"allocations"`
	Pending     int64 `json:"pending"`
}

// SandboxE2BView is the safe E2B selection. It never includes the API key.
type SandboxE2BView struct {
	Template             string `json:"template"`
	APIURL               string `json:"api_url"`
	Domain               string `json:"domain"`
	CredentialConfigured bool   `json:"credential_configured"`
	// The fixed template build as Core read it when this selection was saved.
	TemplateBuild SandboxE2BTemplateBuildView `json:"template_build"`
}

// SandboxE2BTemplateBuildView is the fixed template build as Core read it when
// this selection was saved; GET does not call E2B. Unknown values are null,
// including every value of a selection saved before Core recorded them.
type SandboxE2BTemplateBuildView struct {
	// Build status at selection time; validation admits only ready builds.
	Status    *string                  `json:"status" extensions:"x-nullable"`
	Resources SandboxTemplateResources `json:"resources"`
}

// SandboxTemplateResources uses the specification.resources names. Validation
// requires cpus and memory_mib to equal the selected limits; root_disk_mib is
// the build's native disk size, which Core does not enforce separately.
type SandboxTemplateResources struct {
	CPUs        *int32 `json:"cpus" extensions:"x-nullable"`
	MemoryMiB   *int32 `json:"memory_mib" extensions:"x-nullable"`
	RootDiskMiB *int32 `json:"root_disk_mib" extensions:"x-nullable"`
}

// SandboxSuspensionView is the idle suspension policy. Only microsandbox
// suspends sandboxes; Docker and E2B deployments return null.
type SandboxSuspensionView struct {
	IdleSeconds      int64 `json:"idle_seconds"`
	RetentionSeconds int64 `json:"retention_seconds"`
}
type SandboxNodeRollout struct {
	// Target preparation, independent of an old pin's serving readiness.
	State string `json:"state" enums:"ready,preparing,failed,update_required,unknown"`
	// Durable serving-generation pin; online and provider_ready still gate placement.
	ReadyGeneration *uint64 `json:"ready_generation" extensions:"x-nullable"`
	Diagnostic      string  `json:"diagnostic,omitempty" enums:"provider_unavailable,docker_unavailable,docker_limits_unsupported,runtime_download_failed,runtime_image_unavailable,kvm_unavailable,microsandbox_artifacts_unavailable,capacity_insufficient"`
}
type SandboxRolloutNodes struct {
	Ready          int64 `json:"ready"`
	Preparing      int64 `json:"preparing"`
	Failed         int64 `json:"failed"`
	UpdateRequired int64 `json:"update_required"`
	Unknown        int64 `json:"unknown"`
}
type SandboxRollout struct {
	State                       string               `json:"state" enums:"settled,preparing"`
	PreviousGenerationSandboxes int64                `json:"previous_generation_sandboxes"`
	Nodes                       *SandboxRolloutNodes `json:"nodes" extensions:"x-nullable"`
}
type RuntimeDeploymentView struct {
	Rollout             SandboxRollout             `json:"rollout"`
	Specification       *sandbox.DeploymentSpec    `json:"specification,omitempty"`
	SpecificationDigest string                     `json:"specification_digest,omitempty"`
	Generation          uint64                     `json:"generation"`
	Mode                string                     `json:"mode"`
	Resources           SandboxDeploymentResources `json:"resources"`
	E2B                 *SandboxE2BView            `json:"e2b,omitempty"`
	// Idle suspension policy; microsandbox only, otherwise null.
	Suspension     *SandboxSuspensionView `json:"suspension" extensions:"x-nullable"`
	InstallationID string                 `json:"installation_id"`
	Provider       string                 `json:"provider"`
	Reset          *SandboxResetView      `json:"reset" extensions:"x-nullable"`
	OwnerEpoch     uint64                 `json:"owner_epoch"`
	// Read-only: the installation public URL (OAC_PUBLIC_URL), which nodes and sandboxes use to reach Core. The deployment API does not accept it.
	CoreURL string `json:"core_url"`
}
type RuntimeNodeAllocation struct {
	DeploymentGeneration uint64 `json:"deployment_generation"`
	Diagnostic           string `json:"diagnostic"`
	ID                   string `json:"id"`
	NodeID               string `json:"node_id"`
	TenantID             string `json:"tenant_id"`
	SessionID            string `json:"session_id"`
	EnvironmentID        string `json:"environment_id"`
	State                string `json:"state"`
	ComputePhase         string `json:"compute_phase"`
	// The time the allocation entered its current compute_phase, or null when unknown; an allocation that existed before Core recorded it reports null until its next phase change. For a suspended microsandbox allocation, this time plus the deployment's snapshot retention tells roughly when Core reclaims it.
	ComputePhaseChangedAt *time.Time `json:"compute_phase_changed_at" extensions:"x-nullable"`
	Initialization        string     `json:"initialization"`
	CreatedAt             time.Time  `json:"created_at"`
}
