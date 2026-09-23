package v1

type RuntimeHistoryCapabilities struct {
	Object                string   `json:"object" enums:"agent.runtime_history_capabilities" binding:"required"`
	Available             bool     `json:"available" binding:"required"`
	Reason                *string  `json:"reason" extensions:"x-nullable" binding:"required" enums:"not_configured,periodic_collection_required"`
	CollectionMode        *string  `json:"collection_mode" extensions:"x-nullable" binding:"required" enums:"on_read,periodic"`
	SampleIntervalSeconds *int64   `json:"sample_interval_seconds" extensions:"x-nullable" binding:"required" minimum:"1" maximum:"9007199254740991"`
	RetentionSeconds      *int64   `json:"retention_seconds" extensions:"x-nullable" binding:"required" minimum:"1" maximum:"9007199254740991"`
	MinimumStepSeconds    *int64   `json:"minimum_step_seconds" extensions:"x-nullable" binding:"required" minimum:"1" maximum:"9007199254740991"`
	MaximumRangeSeconds   *int64   `json:"maximum_range_seconds" extensions:"x-nullable" binding:"required" minimum:"1" maximum:"9007199254740991"`
	MaximumPoints         *int     `json:"maximum_points" extensions:"x-nullable" binding:"required" minimum:"2" maximum:"10000"`
	Metrics               []string `json:"metrics" binding:"required" validate:"max=3" enums:"cpu,memory,tokens"`
}

type RuntimeHistory struct {
	Object            string                          `json:"object" enums:"agent.runtime_history" binding:"required"`
	Source            string                          `json:"source" enums:"durable" binding:"required"`
	SessionID         string                          `json:"session_id" binding:"required" format:"uuid"`
	RequestedRange    RuntimeHistoryRange             `json:"requested_range" binding:"required"`
	ResolutionSeconds int64                           `json:"resolution_seconds" binding:"required" minimum:"1" maximum:"9007199254740991"`
	GeneratedAt       int64                           `json:"generated_at" binding:"required" minimum:"0" maximum:"9007199254740991"`
	Coverage          RuntimeHistoryCoverage          `json:"coverage" binding:"required"`
	Series            []RuntimeHistorySeries          `json:"series" binding:"required" validate:"max=1000"`
	TokenUsage        []RuntimeHistoryTokenUsagePoint `json:"token_usage" binding:"required" validate:"max=10000"`
}

// RuntimeHistoryTokenUsagePoint is the final cumulative measured Session usage
// observed in one server-selected bucket. It counts every recorded root Turn
// snapshot, active Turns included, unlike public Session usage.
type RuntimeHistoryTokenUsagePoint struct {
	Start        int64  `json:"start" binding:"required" minimum:"0" maximum:"9007199254740991"`
	End          int64  `json:"end" binding:"required" minimum:"1" maximum:"9007199254740991"`
	SampledAt    int64  `json:"sampled_at" binding:"required" minimum:"0" maximum:"9007199254740991"`
	InputTokens  uint64 `json:"input_tokens" binding:"required" minimum:"0" maximum:"9007199254740991"`
	OutputTokens uint64 `json:"output_tokens" binding:"required" minimum:"0" maximum:"9007199254740991"`
}

type RuntimeHistoryRange struct {
	Start int64 `json:"start" binding:"required" minimum:"0" maximum:"9007199254740991"`
	End   int64 `json:"end" binding:"required" minimum:"1" maximum:"9007199254740991"`
}

type RuntimeHistoryCoverage struct {
	RetainedStart       int64                         `json:"retained_start" binding:"required" minimum:"0" maximum:"9007199254740991"`
	FirstSampleAt       *int64                        `json:"first_sample_at" extensions:"x-nullable" binding:"required" minimum:"0" maximum:"9007199254740991"`
	LastSampleAt        *int64                        `json:"last_sample_at" extensions:"x-nullable" binding:"required" minimum:"0" maximum:"9007199254740991"`
	SampleCount         int64                         `json:"sample_count" binding:"required" minimum:"0" maximum:"9007199254740991"`
	ExpectedSampleCount int64                         `json:"expected_sample_count" binding:"required" minimum:"0" maximum:"9007199254740991"`
	Buckets             []RuntimeHistoryCoveragePoint `json:"buckets" binding:"required" validate:"max=10000"`
}

type RuntimeHistoryCoveragePoint struct {
	Start            int64  `json:"start" binding:"required" minimum:"0" maximum:"9007199254740991"`
	End              int64  `json:"end" binding:"required" minimum:"1" maximum:"9007199254740991"`
	FirstObservedAt  *int64 `json:"first_observed_at" extensions:"x-nullable" binding:"required" minimum:"0" maximum:"9007199254740991"`
	LastObservedAt   *int64 `json:"last_observed_at" extensions:"x-nullable" binding:"required" minimum:"0" maximum:"9007199254740991"`
	ObservationCount int    `json:"observation_count" binding:"required" minimum:"0" maximum:"9007199254740991"`
	ObservedCount    int    `json:"observed_count" binding:"required" minimum:"0" maximum:"9007199254740991"`
	UnavailableCount int    `json:"unavailable_count" binding:"required" minimum:"0" maximum:"9007199254740991"`
}

type RuntimeHistorySeries struct {
	EnvironmentID string                `json:"environment_id" binding:"required" format:"uuid"`
	AllocationID  string                `json:"allocation_id" binding:"required" format:"uuid"`
	StartedAt     RuntimeHistoryTime    `json:"started_at" binding:"required"`
	ProviderType  string                `json:"provider_type" binding:"required" pattern:"^[a-z][a-z0-9_]{0,31}$"`
	Points        []RuntimeHistoryPoint `json:"points" binding:"required" validate:"max=10000"`
}

// RuntimeHistoryTime preserves the earliest retained provider start estimate.
// It is not a per-bucket compute start. AllocationID is the series identity.
type RuntimeHistoryTime struct {
	Seconds     int64 `json:"seconds" binding:"required" minimum:"0" maximum:"9007199254740991"`
	Nanoseconds int   `json:"nanoseconds" binding:"required" minimum:"0" maximum:"999999999"`
}

type RuntimeHistoryPoint struct {
	Start            int64                 `json:"start" binding:"required" minimum:"0" maximum:"9007199254740991"`
	End              int64                 `json:"end" binding:"required" minimum:"1" maximum:"9007199254740991"`
	FirstObservedAt  *int64                `json:"first_observed_at" extensions:"x-nullable" binding:"required" minimum:"0" maximum:"9007199254740991"`
	LastObservedAt   *int64                `json:"last_observed_at" extensions:"x-nullable" binding:"required" minimum:"0" maximum:"9007199254740991"`
	ObservationCount int                   `json:"observation_count" binding:"required" minimum:"0" maximum:"9007199254740991"`
	ObservedCount    int                   `json:"observed_count" binding:"required" minimum:"0" maximum:"9007199254740991"`
	UnavailableCount int                   `json:"unavailable_count" binding:"required" minimum:"0" maximum:"9007199254740991"`
	CPU              *RuntimeHistoryCPU    `json:"cpu" extensions:"x-nullable" binding:"required"`
	Memory           *RuntimeHistoryMemory `json:"memory" extensions:"x-nullable" binding:"required"`
}

type RuntimeHistoryCPU struct {
	ContributorCount int      `json:"contributor_count" binding:"required" minimum:"1" maximum:"9007199254740991"`
	UtilizationRatio *float64 `json:"utilization_ratio" extensions:"x-nullable" binding:"required" minimum:"0"`
	CapacityCores    *float64 `json:"capacity_cores" extensions:"x-nullable" binding:"required" minimum:"5e-324"`
}

type RuntimeHistoryMemory struct {
	ContributorCount int     `json:"contributor_count" binding:"required" minimum:"1" maximum:"9007199254740991"`
	UsageBytes       *uint64 `json:"usage_bytes" extensions:"x-nullable" binding:"required" minimum:"0" maximum:"9007199254740991"`
	LimitBytes       *uint64 `json:"limit_bytes" extensions:"x-nullable" binding:"required" minimum:"1" maximum:"9007199254740991"`
}
