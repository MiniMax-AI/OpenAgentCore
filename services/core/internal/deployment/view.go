package deployment

import (
	"encoding/json"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

type NodeRolloutState string

const (
	NodeRolloutReady          NodeRolloutState = "ready"
	NodeRolloutPreparing      NodeRolloutState = "preparing"
	NodeRolloutFailed         NodeRolloutState = "failed"
	NodeRolloutUpdateRequired NodeRolloutState = "update_required"
	NodeRolloutUnknown        NodeRolloutState = "unknown"
)

type RolloutState string

const (
	RolloutSettled   RolloutState = "settled"
	RolloutPreparing RolloutState = "preparing"
)

// View is the sandbox deployment as administrators read it.
type View struct {
	Rollout              Rollout                 `json:"rollout" binding:"required"`
	Specification        *sandbox.DeploymentSpec `json:"specification,omitempty"`
	SpecificationDigest  string                  `json:"specification_digest,omitempty"`
	Generation           uint64                  `json:"generation" binding:"required"`
	Mode                 sandbox.DeploymentMode  `json:"mode" binding:"required"`
	Resources            Resources               `json:"resources" binding:"required"`
	Configuration        json.RawMessage         `json:"configuration,omitempty" swaggertype:"object"`
	Metadata             json.RawMessage         `json:"metadata,omitempty" swaggertype:"object"`
	CredentialConfigured bool                    `json:"credential_configured" binding:"required"`
	// Idle suspension policy; null unless the selected Provider declares checkpoint support.
	Suspension     *Suspension `json:"suspension" extensions:"x-nullable" binding:"required"`
	InstallationID string      `json:"installation_id" binding:"required"`
	Provider       string      `json:"provider" binding:"required"`
	Reset          *Reset      `json:"reset" extensions:"x-nullable" binding:"required"`
	OwnerEpoch     uint64      `json:"owner_epoch" binding:"required"`
	// Read-only: the installation public URL (OAC_PUBLIC_URL), which nodes and sandboxes use to reach Core. The deployment API does not accept it.
	CoreURL string `json:"core_url" binding:"required"`
}

// Resources counts what still belongs to the deployment.
type Resources struct {
	Allocations int64 `json:"allocations" binding:"required"`
	Pending     int64 `json:"pending" binding:"required"`
}

// Suspension is Core's idle suspension policy, which applies to every Provider
// that declares checkpoint support.
type Suspension struct {
	IdleSeconds      int64 `json:"idle_seconds" binding:"required"`
	RetentionSeconds int64 `json:"retention_seconds" binding:"required"`
}

type NodeRollout struct {
	// Target preparation, independent of an old pin's serving readiness.
	State NodeRolloutState `json:"state" binding:"required"`
	// Durable serving-generation pin; online and provider_ready still gate placement.
	ReadyGeneration *uint64                    `json:"ready_generation" extensions:"x-nullable" binding:"required"`
	Diagnostic      sandbox.NodeDiagnosticCode `json:"diagnostic,omitempty"`
}

type RolloutNodes struct {
	Ready          int64 `json:"ready" binding:"required"`
	Preparing      int64 `json:"preparing" binding:"required"`
	Failed         int64 `json:"failed" binding:"required"`
	UpdateRequired int64 `json:"update_required" binding:"required"`
	Unknown        int64 `json:"unknown" binding:"required"`
}

type Rollout struct {
	State                       RolloutState  `json:"state" binding:"required"`
	PreviousGenerationSandboxes int64         `json:"previous_generation_sandboxes" binding:"required"`
	Nodes                       *RolloutNodes `json:"nodes" extensions:"x-nullable" binding:"required"`
}

// Reset contains only durable state and a single-snapshot resource partition.
type Reset struct {
	Clear       ResetMode      `json:"clear" binding:"required"`
	RequestedAt time.Time      `json:"requested_at" binding:"required"`
	DeadlineAt  *time.Time     `json:"deadline_at" extensions:"x-nullable" binding:"required"`
	ForcedAt    *time.Time     `json:"forced_at" extensions:"x-nullable" binding:"required"`
	Remaining   ResetRemaining `json:"remaining" binding:"required"`
}

type ResetRemaining struct {
	Busy           int64              `json:"busy" binding:"required"`
	Idle           int64              `json:"idle" binding:"required"`
	Cleanup        int64              `json:"cleanup" binding:"required"`
	OnOfflineNodes int64              `json:"on_offline_nodes" binding:"required"`
	OfflineNodes   []ResetOfflineNode `json:"offline_nodes" binding:"required"`
}

type ResetOfflineNode struct {
	NodeID    string `json:"node_id" binding:"required"`
	Name      string `json:"name" binding:"required"`
	Resources int64  `json:"resources" binding:"required"`
}
