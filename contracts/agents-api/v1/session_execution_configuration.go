package v1

// SessionExecutionConfiguration describes committed configuration, not live
// execution health. Unknown provenance is never reconstructed from defaults.
type SessionExecutionConfiguration struct {
	Object        string                     `json:"object" binding:"required" enums:"agent.session.execution_configuration"`
	SchemaVersion int                        `json:"schema_version" binding:"required" enums:"1"`
	SessionID     string                     `json:"session_id" binding:"required"`
	Model         ExecutionSelection         `json:"model" binding:"required"`
	Harness       ExecutionSelection         `json:"harness" binding:"required"`
	ModelProvider ExecutionProviderSelection `json:"model_provider" binding:"required"`
}

type ExecutionSelection struct {
	Value  *string `json:"value" binding:"required" extensions:"x-nullable"`
	Source string  `json:"source" binding:"required" enums:"session,agent,deployment,unknown"`
}

// Deployment credentials have no public endpoint projection. Unavailable means
// no trustworthy safe provider snapshot was recorded, not failed readiness.
type ExecutionProviderSelection struct {
	Source        string             `json:"source" binding:"required" enums:"session,agent,deployment,unknown"`
	Status        string             `json:"status" binding:"required" enums:"available,redacted,unavailable"`
	Configuration *ModelProviderView `json:"configuration" binding:"required" extensions:"x-nullable"`
}
