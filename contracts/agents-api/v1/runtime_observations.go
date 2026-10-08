package v1

type RuntimeObservationMode string

const (
	RuntimeModeNone       RuntimeObservationMode = "none"
	RuntimeModeSelfHosted RuntimeObservationMode = "self_hosted"
	RuntimeModeManaged    RuntimeObservationMode = "openai_hosted"
)

type RuntimeObservationStatus string

const (
	RuntimeStatusObserved    RuntimeObservationStatus = "observed"
	RuntimeStatusUnsupported RuntimeObservationStatus = "unsupported"
	RuntimeStatusUnavailable RuntimeObservationStatus = "unavailable"
)

type RuntimeObservationLifecycleState string

const (
	RuntimeLifecycleActive        RuntimeObservationLifecycleState = "active"
	RuntimeLifecycleSleeping      RuntimeObservationLifecycleState = "sleeping"
	RuntimeLifecycleTransitioning RuntimeObservationLifecycleState = "transitioning"
	RuntimeLifecyclePending       RuntimeObservationLifecycleState = "pending"
	RuntimeLifecycleStopped       RuntimeObservationLifecycleState = "stopped"
)

type RuntimeInstanceKind string

const (
	RuntimeInstanceManagedAllocation    RuntimeInstanceKind = "managed_allocation"
	RuntimeInstanceSelfHostedConnection RuntimeInstanceKind = "self_hosted_connection"
	RuntimeInstanceNone                 RuntimeInstanceKind = "none"
)

type RuntimeObservation struct {
	ID                  string                            `json:"id" binding:"required" format:"uuid"`
	Object              string                            `json:"object" enums:"agent.runtime_observation" binding:"required"`
	SessionID           string                            `json:"session_id" binding:"required" format:"uuid"`
	EnvironmentID       *string                           `json:"environment_id" extensions:"x-nullable" binding:"required" format:"uuid"`
	Mode                RuntimeObservationMode            `json:"mode" binding:"required"`
	ProviderType        *string                           `json:"provider_type" extensions:"x-nullable" binding:"required" pattern:"^[a-z][a-z0-9_]{0,31}$"`
	Instance            RuntimeInstance                   `json:"instance" binding:"required"`
	LifecycleState      *RuntimeObservationLifecycleState `json:"lifecycle_state" extensions:"x-nullable" binding:"required"`
	Status              RuntimeObservationStatus          `json:"status" binding:"required"`
	Reason              *string                           `json:"reason" extensions:"x-nullable" binding:"required" pattern:"^[a-z][a-z0-9_]{0,95}(?![\\s\\S])"`
	AllocationCreatedAt *int64                            `json:"allocation_created_at" extensions:"x-nullable" binding:"required" minimum:"0"`
	ResolvedAt          int64                             `json:"resolved_at" binding:"required" minimum:"0"`
	ObservedAt          *int64                            `json:"observed_at" extensions:"x-nullable" binding:"required" minimum:"0"`
	StartedAt           *int64                            `json:"started_at" extensions:"x-nullable" binding:"required" minimum:"0"`
	CPU                 *RuntimeCPUObservation            `json:"cpu" extensions:"x-nullable" binding:"required"`
	Memory              *RuntimeMemoryObservation         `json:"memory" extensions:"x-nullable" binding:"required"`
}

type RuntimeInstance struct {
	Kind                 RuntimeInstanceKind `json:"kind" binding:"required"`
	AllocationID         *string             `json:"allocation_id" extensions:"x-nullable" binding:"required" format:"uuid"`
	ConnectionGeneration *string             `json:"connection_generation" extensions:"x-nullable" binding:"required" format:"uuid"`
}

type RuntimeCPUObservation struct {
	UsageSecondsTotal *float64 `json:"usage_seconds_total" extensions:"x-nullable" binding:"required" minimum:"0"`
	CapacityCores     *float64 `json:"capacity_cores" extensions:"x-nullable" binding:"required" minimum:"5e-324"`
	UsageCores        *float64 `json:"usage_cores" extensions:"x-nullable" binding:"required" minimum:"0"`
	UtilizationRatio  *float64 `json:"utilization_ratio" extensions:"x-nullable" binding:"required" minimum:"0"`
}

type RuntimeMemoryObservation struct {
	UsageBytes *uint64 `json:"usage_bytes" extensions:"x-nullable" binding:"required" minimum:"0"`
	LimitBytes *uint64 `json:"limit_bytes" extensions:"x-nullable" binding:"required" minimum:"1"`
}
