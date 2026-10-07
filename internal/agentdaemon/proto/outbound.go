package proto

import (
	"encoding/json"

	"github.com/MiniMax-AI/OpenAgentCore/internal/modelprovider"
)

// Type constants for server → daemon frames.
const (
	// TypePromptCancel aborts an in-flight prompt. Envelope.ID =
	// RunID. Idempotent — cancelling an unknown / already-finished
	// run is a no-op on the daemon side.
	TypePromptCancel = "prompt_cancel"
)

// PromptRequestPayload is the execution configuration that execution_prepare
// carries. execution_start supplies the Run identity and input.
type PromptRequestPayload struct {
	MaxConcurrentSubagents *int `json:"max_concurrent_subagents,omitempty"`
	// AgentKind selects which agent implementation the daemon
	// dispatches to.
	AgentKind string `json:"agent_kind"`

	// Model, SystemPrompt, ModelProvider and HarnessConfig are the Session's
	// frozen model configuration. internal/harnessconfig validates them
	// against the selected Harness before any native effect.
	Model         string                  `json:"model,omitempty"`
	SystemPrompt  string                  `json:"system_prompt,omitempty"`
	ModelProvider *modelprovider.Provider `json:"model_provider,omitempty"`
	HarnessConfig HarnessConfig           `json:"harness_config,omitempty"`

	// ExecutionControls are authoritative engine-neutral settings, translated by the adapter.
	ExecutionControls *ExecutionControls `json:"execution_controls,omitempty"`
	// MCPHTTPServers supplies public HTTP declarations with explicit connection origins.
	// Nil preserves existing behavior; an empty list explicitly declares no servers.
	MCPHTTPServers *[]MCPHTTPServer `json:"mcp_http_servers,omitempty"`

	LocalEnvironment *LocalEnvironment `json:"local_environment,omitempty"`

	// AgentSessionID is the upstream engine session id to resume.
	AgentSessionID string `json:"agent_session_id,omitempty"`

	// WorkspaceReadOnly prepares temporary native state that cannot start a Run.
	WorkspaceReadOnly            bool           `json:"workspace_read_only,omitempty"`
	RequireExistingNativeSession bool           `json:"require_existing_native_session,omitempty"`
	ObserveSubagentIdentities    bool           `json:"observe_subagent_identities,omitempty"`
	FunctionTools                []FunctionTool `json:"function_tools,omitempty"`
	ToolSearch                   bool           `json:"tool_search,omitempty"`
	DisableExecutionEnvironment  bool           `json:"disable_execution_environment,omitempty"`
	DisableSubagents             bool           `json:"disable_subagents,omitempty"`
}

// PromptCancelPayload optionally requests an application receipt; identity is on Envelope.ID.
type PromptCancelPayload struct {
	DeliveryID string `json:"delivery_id,omitempty"`
}

// ExecutionControls requires text_verbosity when supplied; omitting the block
// keeps native defaults. Native web search is always disabled.
type ExecutionControls struct {
	DisableProgrammaticToolCalling bool          `json:"disable_programmatic_tool_calling,omitempty"`
	TextVerbosity                  string        `json:"text_verbosity"`
	OutputFormat                   *OutputFormat `json:"output_format,omitempty"`
}

// OutputFormat passes the public schema unchanged to a qualified native adapter.
type OutputFormat struct {
	Type   string          `json:"type"`
	Schema json.RawMessage `json:"schema"`
}
