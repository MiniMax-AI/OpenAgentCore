package v1

type RuntimeObservation struct {
	ID                  string                    `json:"id" binding:"required" format:"uuid"`
	Object              string                    `json:"object" enums:"agent.runtime_observation" binding:"required"`
	SessionID           string                    `json:"session_id" binding:"required" format:"uuid"`
	EnvironmentID       *string                   `json:"environment_id" extensions:"x-nullable" binding:"required" format:"uuid"`
	Mode                string                    `json:"mode" enums:"none,self_hosted,openai_hosted" binding:"required"`
	ProviderType        *string                   `json:"provider_type" extensions:"x-nullable" binding:"required" pattern:"^[a-z][a-z0-9_]{0,31}$"`
	Instance            RuntimeInstance           `json:"instance" binding:"required"`
	Status              string                    `json:"status" enums:"observed,unsupported,unavailable" binding:"required"`
	Reason              *string                   `json:"reason" extensions:"x-nullable" binding:"required" enums:"runtime_mode_not_observable,allocation_pending,runtime_not_running,source_not_configured,sample_timeout,sample_unavailable"`
	AllocationCreatedAt *int64                    `json:"allocation_created_at" extensions:"x-nullable" binding:"required" minimum:"0"`
	ResolvedAt          int64                     `json:"resolved_at" binding:"required" minimum:"0"`
	ObservedAt          *int64                    `json:"observed_at" extensions:"x-nullable" binding:"required" minimum:"0"`
	StartedAt           *int64                    `json:"started_at" extensions:"x-nullable" binding:"required" minimum:"0"`
	CPU                 *RuntimeCPUObservation    `json:"cpu" extensions:"x-nullable" binding:"required"`
	Memory              *RuntimeMemoryObservation `json:"memory" extensions:"x-nullable" binding:"required"`
}

type RuntimeInstance struct {
	Kind                 string  `json:"kind" enums:"managed_allocation,self_hosted_connection,none" binding:"required"`
	AllocationID         *string `json:"allocation_id" extensions:"x-nullable" binding:"required" format:"uuid"`
	DeviceID             *string `json:"device_id" extensions:"x-nullable" binding:"required" format:"uuid"`
	ConnectionGeneration *string `json:"connection_generation" extensions:"x-nullable" binding:"required" format:"uuid"`
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

type RuntimeObservationList struct {
	Object  string               `json:"object" enums:"list" binding:"required"`
	Data    []RuntimeObservation `json:"data" binding:"required"`
	HasMore bool                 `json:"has_more" binding:"required"`
	FirstID *string              `json:"first_id" extensions:"x-nullable" binding:"required" format:"uuid"`
	LastID  *string              `json:"last_id" extensions:"x-nullable" binding:"required" format:"uuid"`
}
