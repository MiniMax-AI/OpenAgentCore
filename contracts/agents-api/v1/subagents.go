package v1

// AgentContent is the pinned content union used for inter-agent instructions and messages.
type AgentContent struct {
	Type             string  `json:"type" binding:"required" enums:"output_text,encrypted_content"`
	Text             *string `json:"text,omitempty"`
	EncryptedContent *string `json:"encrypted_content,omitempty"`
}

type Subagent struct {
	ID            string         `json:"id" binding:"required"`
	Object        string         `json:"object" binding:"required" enums:"agent.session.subagent"`
	SessionID     string         `json:"session_id" binding:"required"`
	ParentAgentID string         `json:"parent_agent_id" binding:"required"`
	OpenedAt      int64          `json:"opened_at" binding:"required"`
	ClosedAt      *int64         `json:"closed_at" extensions:"x-nullable"`
	Name          *string        `json:"name" extensions:"x-nullable"`
	Instructions  []AgentContent `json:"instructions" extensions:"x-nullable"`
	Status        string         `json:"status" binding:"required" enums:"active,closed"`
}

type SubagentList struct {
	Data    []Subagent `json:"data" binding:"required"`
	HasMore bool       `json:"has_more" binding:"required"`
}
