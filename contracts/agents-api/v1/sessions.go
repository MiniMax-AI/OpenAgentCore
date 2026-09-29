// Package v1 contains the supported wire types from the pinned Agents API.
package v1

import "encoding/json"

// CreateSessionRequest supports inline configuration or a saved Agent reference.
// Initial text input is accepted with ordinary or streaming responses.
type CreateSessionRequest struct {
	XAgentsCore *SessionExecutionInput `json:"x_agents_core,omitempty"`
	Agent       *InlineAgent           `json:"agent,omitempty"`
	AgentID     *string                `json:"agent_id,omitempty"`
	Environment *Environment           `json:"environment" binding:"required"`
	// Input accepts a string or an ordered array of user InputMessage objects.
	// Required for none and streamed creation outside self_hosted; otherwise optional.
	Input    any               `json:"input,omitempty" extensions:"x-nullable"`
	Metadata map[string]string `json:"metadata,omitempty" extensions:"x-nullable"`
	Stream   bool              `json:"stream,omitempty" default:"false"`
	VaultIDs []string          `json:"vault_ids,omitempty"`
}

// UpdateSessionRequest requires metadata; null and an empty object clear it.
type UpdateSessionRequest struct {
	Metadata map[string]string `json:"metadata" extensions:"x-nullable" binding:"required"`
}

// InlineAgent supplies a complete inline configuration or per-Session overrides.
// With agent_id, omitted fields inherit and supplied fields replace saved values.
type InlineAgent struct {
	XAgentsCore  *AgentsCore          `json:"x_agents_core,omitempty" extensions:"x-nullable"`
	Model        *string              `json:"model,omitempty"`
	Instructions *string              `json:"instructions,omitempty" extensions:"x-nullable"`
	MultiAgent   json.RawMessage      `json:"multi_agent,omitempty" swaggertype:"object" extensions:"x-nullable"`
	Reasoning    *Reasoning           `json:"reasoning,omitempty" extensions:"x-nullable"`
	ServiceTier  *string              `json:"service_tier,omitempty" enums:"auto,default,flex,priority,fast" extensions:"x-nullable"`
	Text         *SavedAgentTextInput `json:"text,omitempty" extensions:"x-nullable"`
	Tools        []json.RawMessage    `json:"tools,omitempty" swaggertype:"array,object" extensions:"x-nullable"`
}

// Environment contains supported request variants; self-hosted creation requires a workspace directory.
type Environment struct {
	Plugins               []json.RawMessage        `json:"plugins,omitempty" swaggertype:"array,object" extensions:"x-nullable"`
	Skills                []json.RawMessage        `json:"skills,omitempty" swaggertype:"array,object" extensions:"x-nullable"`
	Env                   map[string]string        `json:"env,omitempty" extensions:"x-nullable"`
	SetupCommands         []json.RawMessage        `json:"setup_commands,omitempty" extensions:"x-nullable" swaggertype:"array,object"`
	Packages              *EnvironmentPackages     `json:"packages,omitempty" extensions:"x-nullable"`
	Files                 []json.RawMessage        `json:"files,omitempty" swaggertype:"array,object" extensions:"x-nullable"`
	EnvironmentTemplateID string                   `json:"environment_template_id,omitempty"`
	Type                  string                   `json:"type" enums:"none,self_hosted,openai_hosted" binding:"required"`
	WorkspaceDirectory    string                   `json:"workspace_directory,omitempty"`
	CapabilityDirectories []string                 `json:"capability_directories,omitempty" extensions:"x-nullable"`
	Network               *EnvironmentNetworkInput `json:"network,omitempty" extensions:"x-nullable"`
}

type Agent struct {
	XAgentsCore  *AgentsCore       `json:"x_agents_core,omitempty" extensions:"x-nullable"`
	ID           string            `json:"id" binding:"required"`
	Instructions *string           `json:"instructions" extensions:"x-nullable"`
	Model        string            `json:"model" binding:"required"`
	MultiAgent   MultiAgentConfig  `json:"multi_agent" binding:"required"`
	Name         *string           `json:"name" extensions:"x-nullable"`
	Reasoning    Reasoning         `json:"reasoning" binding:"required"`
	ServiceTier  string            `json:"service_tier" enums:"auto" binding:"required"`
	Text         TextConfig        `json:"text" binding:"required"`
	Tools        []json.RawMessage `json:"tools" swaggertype:"array,object" binding:"required"`
}

type MultiAgentConfig struct {
	Enabled                bool `json:"enabled" binding:"required"`
	MaxConcurrentSubagents *int `json:"max_concurrent_subagents" extensions:"x-nullable"`
}

type Reasoning struct {
	Effort  *string `json:"effort,omitempty" extensions:"x-nullable"`
	Summary *string `json:"summary,omitempty" extensions:"x-nullable"`
}

// reasoningResponse renders both reasoning keys, null when unset, as pinned
// Agent and Session responses do (SES-23, VA-11). Requests and stored
// configuration keep the omitting Reasoning encoding. It does not resolve a
// model-derived default effort.
type reasoningResponse struct {
	Effort  *string `json:"effort"`
	Summary *string `json:"summary"`
}

type TextConfigInput struct {
	Format    *TextFormat `json:"format,omitempty" extensions:"x-nullable"`
	Verbosity *string     `json:"verbosity,omitempty" enums:"low,medium,high" extensions:"x-nullable"`
}

type TextConfig struct {
	Format    TextFormat `json:"format" binding:"required"`
	Verbosity string     `json:"verbosity" enums:"low,medium,high" binding:"required"`
}

type TextFormat struct {
	Type   string          `json:"type" enums:"text,json_schema" binding:"required"`
	Schema json.RawMessage `json:"schema,omitempty" swaggertype:"object"`
}

type Session struct {
	XAgentsCore     *SessionCore       `json:"x_agents_core,omitempty"`
	ID              string             `json:"id" binding:"required"`
	Agent           Agent              `json:"agent" binding:"required"`
	CreatedAt       int64              `json:"created_at" binding:"required"`
	Environment     SessionEnvironment `json:"environment" binding:"required"`
	Error           *string            `json:"error" extensions:"x-nullable"`
	LastActiveAt    int64              `json:"last_active_at" binding:"required"`
	Metadata        map[string]string  `json:"metadata" binding:"required"`
	Object          string             `json:"object" enums:"agent.session" binding:"required"`
	RequiredActions []RequiredAction   `json:"required_actions" binding:"required"`
	Status          string             `json:"status" enums:"idle,in_progress,requires_action,failed" binding:"required"`
	Usage           *TokenUsage        `json:"usage" extensions:"x-nullable"`
	VaultIDs        []string           `json:"vault_ids" binding:"required"`
}

type agentWire Agent

// agentResponse renders a Session Agent with explicit reasoning keys.
type agentResponse struct {
	agentWire
	Reasoning reasoningResponse `json:"reasoning"`
}

// MarshalJSON renders the Session Agent's reasoning with explicit null keys.
// The stored Session configuration keeps its original encoding.
func (s Session) MarshalJSON() ([]byte, error) {
	type wire Session
	return json.Marshal(struct {
		wire
		Agent agentResponse `json:"agent"`
	}{wire(s), agentResponse{agentWire(s.Agent), reasoningResponse(s.Agent.Reasoning)}})
}

type SessionList struct {
	Object  string    `json:"object" enums:"list" binding:"required"`
	FirstID *string   `json:"first_id" extensions:"x-nullable"`
	LastID  *string   `json:"last_id" extensions:"x-nullable"`
	Data    []Session `json:"data" binding:"required"`
	HasMore bool      `json:"has_more" binding:"required"`
}

type ErrorResponse struct {
	Error APIError `json:"error" binding:"required"`
}

type APIError struct {
	Message string  `json:"message" binding:"required"`
	Type    string  `json:"type" binding:"required"`
	Code    *string `json:"code" extensions:"x-nullable"`
	Param   *string `json:"param" extensions:"x-nullable"`
}
