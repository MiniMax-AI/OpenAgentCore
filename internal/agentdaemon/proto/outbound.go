package proto

import (
	"encoding/json"

	"github.com/MiniMax-AI/OpenAgentCore/internal/modelprovider"
)

// Type constants for server → daemon frames.
const (
	// TypePromptRequest triggers one prompt cycle. Envelope.ID = RunID;
	// the daemon stamps every resulting upstream frame with the same
	// ID so the gateway can fan them back to the matching StreamPrompt
	// subscriber.
	TypePromptRequest = "prompt_request"

	// TypePromptCancel aborts an in-flight prompt. Envelope.ID =
	// RunID. Idempotent — cancelling an unknown / already-finished
	// run is a no-op on the daemon side.
	TypePromptCancel = "prompt_cancel"

	// TypePermissionDecision delivers a human verdict back to the
	// daemon. Envelope.ID = the perm_<8hex> id the daemon minted in
	// the matching permission_request.
	TypePermissionDecision = "permission_decision"

	// TypePromptForUserChoiceDecision delivers the human's answer back
	// to the daemon. Envelope.ID = the ask_<8hex> id the daemon minted
	// in the matching prompt_for_user_choice frame.
	TypePromptForUserChoiceDecision = "prompt_for_user_choice_decision"
)

// PromptRequestPayload is the daemon-side view of a connector.PromptInput,
// trimmed to fields a daemon agent actually needs. Kept separate from
// PromptInput so future agent implementations can evolve the wire shape
// without touching the connector surface.
type PromptRequestPayload struct {
	MaxConcurrentSubagents *int `json:"max_concurrent_subagents,omitempty"`
	// AgentKind selects which agent implementation the daemon
	// dispatches to.
	AgentKind string `json:"agent_kind"`

	// ConversationID lets the daemon scope per-conversation state
	// (Claude --resume session id, scratch dir).
	ConversationID string `json:"conversation_id"`

	// RunID is the ID of the Core Turn this prompt executes; mirrored
	// back on every upstream frame via Envelope.ID.
	RunID string `json:"run_id"`

	// Input preserves ordered user messages and content.
	Input MessageInput `json:"input,omitempty"`

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

	// AgentStateKey is the stable daemon-side state directory key.
	// WorkspaceReadOnly prepares temporary native state that cannot start a Run.
	WorkspaceReadOnly bool   `json:"workspace_read_only,omitempty"`
	AgentStateKey     string `json:"agent_state_key,omitempty"`
	// ReleaseOnCompletion closes the native writer before acknowledging Done.
	ReleaseOnCompletion          bool `json:"release_on_completion,omitempty"`
	StrictResume                 bool `json:"strict_resume,omitempty"`
	RequireExistingNativeSession bool `json:"require_existing_native_session,omitempty"`
	ObserveMessages              bool `json:"observe_messages,omitempty"`

	ObserveToolObservations     bool           `json:"observe_tool_observations,omitempty"`
	ObserveSubagentIdentities   bool           `json:"observe_subagent_identities,omitempty"`
	FunctionTools               []FunctionTool `json:"function_tools,omitempty"`
	ToolSearch                  bool           `json:"tool_search,omitempty"`
	DisableExecutionEnvironment bool           `json:"disable_execution_environment,omitempty"`
	DisableSubagents            bool           `json:"disable_subagents,omitempty"`
}

// PromptCancelPayload optionally requests an application receipt; identity is on Envelope.ID.
type PromptCancelPayload struct {
	DeliveryID string `json:"delivery_id,omitempty"`
}

// PermissionDecisionPayload carries the human verdict. UpdatedInput
// lets the approver edit the tool input before letting the call
// proceed (Claude Code's allow-with-changes path).
type PermissionDecisionPayload struct {
	DeliveryID   string         `json:"delivery_id"`
	Approved     bool           `json:"approved"`
	Message      string         `json:"message,omitempty"`
	UpdatedInput map[string]any `json:"updated_input,omitempty"`
}

// PromptForUserChoiceQuestionAnswer binds answer values to one emitted question ID.
// Headers and array positions never identify a question.
type PromptForUserChoiceQuestionAnswer struct {
	QuestionID string   `json:"question_id"`
	Answers    []string `json:"answers"`
}

// PromptForUserChoiceDecisionPayload carries either explicitly identified answers
// or cancellation. Omitted questions remain unanswered; adapters never reassign
// answers by position or display text. Native choice validation stays in the adapter.
type PromptForUserChoiceDecisionPayload struct {
	DeliveryID      string                              `json:"delivery_id"`
	QuestionAnswers []PromptForUserChoiceQuestionAnswer `json:"question_answers,omitempty"`
	Cancelled       bool                                `json:"cancelled,omitempty"`
	Reason          string                              `json:"reason,omitempty"`
}

// ExecutionControls requires both values when supplied; omitting the block keeps native defaults.
// Send only to a peer advertising execution_controls.
type ExecutionControls struct {
	DisableProgrammaticToolCalling bool          `json:"disable_programmatic_tool_calling,omitempty"`
	WebSearch                      string        `json:"web_search"`
	TextVerbosity                  string        `json:"text_verbosity"`
	OutputFormat                   *OutputFormat `json:"output_format,omitempty"`
}

// OutputFormat passes the public schema unchanged to a qualified native adapter.
type OutputFormat struct {
	Type   string          `json:"type"`
	Schema json.RawMessage `json:"schema"`
}
