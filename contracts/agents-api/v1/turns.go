package v1

type Turn struct {
	ID          string      `json:"id" binding:"required"`
	AgentID     string      `json:"agent_id" binding:"required"`
	SubagentID  *string     `json:"subagent_id" extensions:"x-nullable"`
	SessionID   string      `json:"session_id" binding:"required"`
	Object      string      `json:"object" enums:"agent.session.turn" binding:"required"`
	Status      string      `json:"status" enums:"queued,in_progress,waiting,completed,failed,cancelled" binding:"required"`
	CreatedAt   int64       `json:"created_at" binding:"required"`
	StartedAt   *int64      `json:"started_at" extensions:"x-nullable"`
	CompletedAt *int64      `json:"completed_at" extensions:"x-nullable"`
	Error       *TurnError  `json:"error" extensions:"x-nullable"`
	Usage       *TokenUsage `json:"usage" extensions:"x-nullable"`
}

type TurnError struct {
	Code    string `json:"code" enums:"context_length_exceeded,session_budget_exceeded,usage_limit_exceeded,rate_limit_exceeded,server_overloaded,cyber_policy,connection_failed,server_error,authentication_error,invalid_request,resource_not_found,sandbox_error,executor_version_incompatible,active_turn_not_steerable,request_timeout,internal_error" binding:"required"`
	Message string `json:"message" binding:"required"`
}

type TurnList struct {
	Data    []Turn `json:"data" binding:"required"`
	HasMore bool   `json:"has_more" binding:"required"`
}
