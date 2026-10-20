package proto

import (
	"encoding/json"
	"errors"
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

	// RunID and Input are empty in an execution_prepare configuration.
	// Adapters set them from execution_start when they start a Turn.
	RunID string `json:"run_id"`

	// Input preserves ordered user messages and content.
	Input MessageInput `json:"input,omitempty"`

	// AgentOptions carries only the keys ValidateAgentOptions accepts. The
	// daemon's agent interprets them; the gateway never inspects them.
	AgentOptions map[string]any `json:"agent_options,omitempty"`

	// ExecutionControls are authoritative engine-neutral settings, translated by the adapter.
	ExecutionControls *ExecutionControls `json:"execution_controls,omitempty"`
	// MCPHTTPServers supplies public HTTP declarations with explicit connection origins.
	// Nil preserves existing behavior; an empty list explicitly declares no servers.
	MCPHTTPServers *[]MCPHTTPServer `json:"mcp_http_servers,omitempty"`

	LocalEnvironment *LocalEnvironment `json:"local_environment,omitempty"`

	// AgentSessionID is the upstream engine session id to resume.
	AgentSessionID string `json:"agent_session_id,omitempty"`

	// AgentStateKey is the stable daemon-side state directory key.
	// WorkspaceReadOnly prepares temporary native state that cannot start a Run.
	WorkspaceReadOnly            bool           `json:"workspace_read_only,omitempty"`
	AgentStateKey                string         `json:"agent_state_key,omitempty"`
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

// ExecutionControls carries the official execution options the Session sets or
// whose official default applies. An absent member means the Harness's native
// default: Core never sends a value the official configuration does not define.
// Send only to a peer advertising execution_controls.
type ExecutionControls struct {
	WebSearch *WebSearchControl `json:"web_search,omitempty"`
	// ProgrammaticToolCalling is the official programmatic_tool_calling.enabled value.
	ProgrammaticToolCalling *bool  `json:"programmatic_tool_calling,omitempty"`
	ReasoningEffort         string `json:"reasoning_effort,omitempty"`
	ReasoningSummary        string `json:"reasoning_summary,omitempty"`
	// ServiceTier is never "auto": auto means unset.
	ServiceTier   string        `json:"service_tier,omitempty"`
	TextVerbosity string        `json:"text_verbosity,omitempty"`
	OutputFormat  *OutputFormat `json:"output_format,omitempty"`
}

// WebSearchControl is the resolved official web_search tool. A nil
// AllowedDomains is the official null; an empty list is kept distinct.
type WebSearchControl struct {
	Mode           string          `json:"mode"`
	ContextSize    string          `json:"context_size"`
	AllowedDomains []string        `json:"allowed_domains"`
	Location       *SearchLocation `json:"location,omitempty"`
}

// SearchLocation carries only the keys the Session set.
type SearchLocation struct {
	City     *string `json:"city,omitempty"`
	Country  *string `json:"country,omitempty"`
	Region   *string `json:"region,omitempty"`
	Timezone *string `json:"timezone,omitempty"`
}

// OutputFormat passes the public schema unchanged to a qualified native adapter.
type OutputFormat struct {
	Type   string          `json:"type"`
	Schema json.RawMessage `json:"schema"`
}

// ValidateAgentOptions rejects any agent_options key other than model,
// system_prompt, model_provider and harness_config.
func (r PromptRequestPayload) ValidateAgentOptions() error {
	for key := range r.AgentOptions {
		switch key {
		case "model", "system_prompt", "model_provider", "harness_config":
		default:
			return errors.New("unsupported agent option")
		}
	}
	return nil
}
