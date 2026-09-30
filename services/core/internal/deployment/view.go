package deployment

import (
	"encoding/json"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

// View is the sandbox deployment as administrators read it.
type View struct {
	Rollout              Rollout                 `json:"rollout"`
	Specification        *sandbox.DeploymentSpec `json:"specification,omitempty"`
	SpecificationDigest  string                  `json:"specification_digest,omitempty"`
	Generation           uint64                  `json:"generation"`
	Mode                 string                  `json:"mode"`
	Resources            Resources               `json:"resources"`
	Configuration        json.RawMessage         `json:"configuration,omitempty" swaggertype:"object"`
	Metadata             json.RawMessage         `json:"metadata,omitempty" swaggertype:"object"`
	CredentialConfigured bool                    `json:"credential_configured"`
	// Idle suspension policy; microsandbox only, otherwise null.
	Suspension     *Suspension `json:"suspension" extensions:"x-nullable"`
	InstallationID string      `json:"installation_id"`
	Provider       string      `json:"provider"`
	Reset          *Reset      `json:"reset" extensions:"x-nullable"`
	OwnerEpoch     uint64      `json:"owner_epoch"`
	// Read-only: the installation public URL (OAC_PUBLIC_URL), which nodes and sandboxes use to reach Core. The deployment API does not accept it.
	CoreURL string `json:"core_url"`
}

// Resources counts what still belongs to the deployment.
type Resources struct {
	Allocations int64 `json:"allocations"`
	Pending     int64 `json:"pending"`
}

// Suspension is the idle suspension policy. Only microsandbox suspends
// sandboxes; Docker and E2B deployments return null.
type Suspension struct {
	IdleSeconds      int64 `json:"idle_seconds"`
	RetentionSeconds int64 `json:"retention_seconds"`
}

type NodeRollout struct {
	// Target preparation, independent of an old pin's serving readiness.
	State string `json:"state" enums:"ready,preparing,failed,update_required,unknown"`
	// Durable serving-generation pin; online and provider_ready still gate placement.
	ReadyGeneration *uint64 `json:"ready_generation" extensions:"x-nullable"`
	Diagnostic      string  `json:"diagnostic,omitempty" enums:"provider_unavailable,docker_unavailable,docker_limits_unsupported,runtime_download_failed,runtime_image_unavailable,kvm_unavailable,microsandbox_artifacts_unavailable,capacity_insufficient"`
}

type RolloutNodes struct {
	Ready          int64 `json:"ready"`
	Preparing      int64 `json:"preparing"`
	Failed         int64 `json:"failed"`
	UpdateRequired int64 `json:"update_required"`
	Unknown        int64 `json:"unknown"`
}

type Rollout struct {
	State                       string        `json:"state" enums:"settled,preparing"`
	PreviousGenerationSandboxes int64         `json:"previous_generation_sandboxes"`
	Nodes                       *RolloutNodes `json:"nodes" extensions:"x-nullable"`
}

// Reset contains only durable state and a single-snapshot resource partition.
type Reset struct {
	Clear       string         `json:"clear"`
	RequestedAt time.Time      `json:"requested_at"`
	DeadlineAt  *time.Time     `json:"deadline_at" extensions:"x-nullable"`
	ForcedAt    *time.Time     `json:"forced_at" extensions:"x-nullable"`
	Remaining   ResetRemaining `json:"remaining"`
}

type ResetRemaining struct {
	Busy           int64              `json:"busy"`
	Idle           int64              `json:"idle"`
	Cleanup        int64              `json:"cleanup"`
	OnOfflineNodes int64              `json:"on_offline_nodes"`
	OfflineNodes   []ResetOfflineNode `json:"offline_nodes"`
}

type ResetOfflineNode struct {
	NodeID    string `json:"node_id"`
	Name      string `json:"name"`
	Resources int64  `json:"resources"`
}
