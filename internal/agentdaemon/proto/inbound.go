package proto

// This package lives at the repo-root module so both the server-side
// gateway/connector AND apps/daemon can import it. That rules out
// importing server/internal/... (Go's internal-package rule), so wire
// types like Usage are declared here in full rather than imported from
// store.UsageInput. The connector layer translates at the boundary;
// the wire schema stays decoupled from upstream Go type edits.

// Type constants for daemon → server frames. Names match
// connector.PromptEvent.Type 1:1 so the gateway can translate without
// a per-event lookup table.
const (
	// TypeDelta carries an incremental text fragment. Daemon
	// accumulates these so the matching done frame can carry the
	// full Final.Content.
	TypeDelta = "delta"

	// TypeOutputMessage carries opt-in native message boundaries and completion snapshots.
	TypeOutputMessage = "output_message"

	// TypeThinking carries an internal-thinking fragment. Gateway
	// forwards as a plain EventDelta so existing renderers keep
	// working.
	TypeThinking = "thinking"

	// TypeToolCall carries a tool invocation. Stage=="before" runs
	// before the call, "after" runs after.
	TypeToolCall = "tool_call"

	// TypeInteractionDecisionAck confirms that the daemon-side agent
	// accepted (or definitively rejected) a function result or cancellation.
	// The server must not mark the delivery terminal before this frame arrives.
	TypeInteractionDecisionAck = "interaction_decision_ack"

	// TypeUsage reports a cumulative usage snapshot for the current execution.
	// Repeated snapshots, including the final Done snapshot, replace rather than add.
	TypeUsage = "usage"

	// TypeError signals the prompt failed. Daemon MUST emit a Done
	// frame immediately after to close the stream.
	TypeError = "error"

	// TypeDone signals the prompt completed. Payload carries the
	// equivalent of a sync PromptOutput.
	TypeDone = "done"

	// TypeHeartbeat is the daemon's liveness signal. Carries no ID.
	// Gateway uses arrival time to detect dead sessions.
	TypeHeartbeat = "heartbeat"
)

// DeltaPayload carries an incremental text fragment from the agent.
type DeltaPayload struct {
	ItemID   string `json:"item_id,omitempty"`
	Delta    string `json:"delta"`
	Sequence uint64 `json:"sequence"`
}

// OutputMessagePayload describes a native assistant message; Text is a completion snapshot.
type OutputMessagePayload struct {
	ID     string  `json:"id"`
	Status string  `json:"status"`
	Phase  string  `json:"phase,omitempty"`
	Text   *string `json:"text,omitempty"`
}

// ThinkingPayload carries an internal-thinking fragment.
type ThinkingPayload struct {
	Text     string `json:"text"`
	Sequence uint64 `json:"sequence,omitempty"`
}

// ToolCallPayload carries a tool invocation event. Stage is "before"
// when the agent is about to call the tool, "after" when the result
// is back.
type ToolCallPayload struct {
	Observation *ToolObservation `json:"observation,omitempty"`
	ID          string           `json:"id"`
	Name        string           `json:"name"`
	Stage       string           `json:"stage"`
	Args        map[string]any   `json:"args,omitempty"`
	Result      map[string]any   `json:"result,omitempty"`
}

// InteractionDecisionAckPayload is the daemon's application-level receipt
// for a server decision. DeliveryID correlates one resolve attempt without
// relying on the request id, which may outlive a reconnect or timeout race.
type InteractionDecisionAckPayload struct {
	DeliveryID string `json:"delivery_id"`
	Applied    bool   `json:"applied"`
	ErrorCode  string `json:"error_code,omitempty"`
	Error      string `json:"error,omitempty"`
	// Outcome preserves native continuity when cancellation does not emit Done.
	Outcome *DonePayload `json:"outcome,omitempty"`
}

// TokenUsage is a complete cumulative measurement for the current execution.
// Adapters publish observed snapshots promptly; historical resume totals belong
// to the adapter baseline, not this execution. Unknown fields must not become zero.
type TokenUsage struct {
	InputTokens           int64 `json:"input_tokens"`
	CachedInputTokens     int64 `json:"cached_input_tokens"`
	OutputTokens          int64 `json:"output_tokens"`
	ReasoningOutputTokens int64 `json:"reasoning_output_tokens"`
	TotalTokens           int64 `json:"total_tokens"`
}

// Usage retains native evidence independently of the optional public breakdown.
// Tokens is absent when a complete, correctly scoped measurement is unavailable.
// Core does not reconstruct token counts, prices or provider identity from Raw.
type Usage struct {
	Tokens       *TokenUsage    `json:"tokens,omitempty"`
	Provider     string         `json:"provider,omitempty"`
	Model        string         `json:"model,omitempty"`
	InputTokens  int32          `json:"input_tokens,omitempty"`
	OutputTokens int32          `json:"output_tokens,omitempty"`
	CostUSD      float64        `json:"cost_usd,omitempty"`
	Raw          map[string]any `json:"raw,omitempty"`
}

// UsagePayload carries a Usage update mid-stream.
type UsagePayload struct {
	Usage
}

// ErrorPayload reports a prompt-level failure.
type ErrorPayload struct {
	Error      string `json:"error"`
	Code       string `json:"code,omitempty"`
	HTTPStatus *int   `json:"http_status,omitempty"`
}

// DonePayload mirrors connector.PromptOutput shape. Redeclared (not
// embedded) so a refactor of PromptOutput doesn't silently flip the
// wire shape.
type DonePayload struct {
	// SourceCompletedAtMS freezes the native root completion before child settlement.
	SourceCompletedAtMS *int64         `json:"source_completed_at_ms,omitempty"`
	Content             string         `json:"content"`
	Transcript          string         `json:"transcript,omitempty"`
	Usage               Usage          `json:"usage,omitzero"`
	Metadata            map[string]any `json:"metadata,omitempty"`
}

const (
	DoneMetaAgentSessionID   = "agent_session_id"
	DoneMetaAgentSessionType = "agent_session_type"
)

// AgentKindCapabilities describes what a daemon-side agent_kind can
// do inside one prompt session. Every field requires an explicit support
// decision, including for unavailable engines. Runtime lifecycle requirements
// are mandatory independently of these optional operations.
type AgentKindCapabilities struct {
	SubagentObservations  CapabilitySupport `json:"subagent_observations"`
	Streaming             CapabilitySupport `json:"streaming"`
	Usage                 CapabilitySupport `json:"usage"`
	Resume                CapabilitySupport `json:"resume"`
	NativeSessionRecovery CapabilitySupport `json:"native_session_recovery"`
	// RetainedNativeHistory resumes the same native Session after confirmed
	// process shutdown using its retained private state, without replaying input.
	// It does not promise process memory or external connection restoration.
	RetainedNativeHistory CapabilitySupport `json:"retained_native_history"`
	Steering              CapabilitySupport `json:"steering"`
	MessageItems          CapabilitySupport `json:"message_items"`

	ToolObservations               CapabilitySupport `json:"tool_observations"`
	EnvironmentNone                CapabilitySupport `json:"environment_none"`
	LocalEnvironment               CapabilitySupport `json:"local_environment"`
	Preparation                    CapabilitySupport `json:"preparation"`
	WorkspaceReadPreparation       CapabilitySupport `json:"workspace_read_preparation"`
	WorkspaceOutputExport          CapabilitySupport `json:"workspace_output_export"`
	ProgrammaticToolCallingDisable CapabilitySupport `json:"programmatic_tool_calling_disable"`
	WebSearchControl               CapabilitySupport `json:"web_search_control"`
	// ExecutionControls supports typed search and verbosity controls.
	ExecutionControls    CapabilitySupport `json:"execution_controls"`
	TextVerbosity        CapabilitySupport `json:"text_verbosity"`
	StructuredOutput     CapabilitySupport `json:"structured_output"`
	ToolSearch           CapabilitySupport `json:"tool_search"`
	MessageImages        CapabilitySupport `json:"message_images"`
	FunctionResultImages CapabilitySupport `json:"function_result_images"`
	SubagentControl      CapabilitySupport `json:"subagent_control"`
	DurableInputReceipts CapabilitySupport `json:"durable_input_receipts"`
	// DurableTurns includes strict resume, completion release and cancellation snapshots.
	DurableTurns      CapabilitySupport `json:"durable_turns"`
	FunctionTools     CapabilitySupport `json:"function_tools"`
	MCPHTTPTools      CapabilitySupport `json:"mcp_http_tools"`
	MCPHTTPRequired   CapabilitySupport `json:"mcp_http_required"`
	MCPHTTPBearerAuth CapabilitySupport `json:"mcp_http_bearer_auth"`
}

// SupportedAgentKind is one daemon-advertised agent engine. Daemons
// can report unavailable kinds with Available=false when the adapter
// exists but the underlying CLI binary is missing.
type SupportedAgentKind struct {
	Kind         string                `json:"kind"`
	Available    bool                  `json:"available"`
	Version      string                `json:"version,omitempty"`
	Capabilities AgentKindCapabilities `json:"capabilities"`
}

// HeartbeatPayload advertises only explicit engine descriptors. Missing
// supported_agent_kinds establishes no engine availability or capabilities.
type HeartbeatPayload struct {
	Timestamp           int64                `json:"ts"`
	ActiveRequests      int                  `json:"active_requests"`
	DaemonVersion       string               `json:"daemon_version,omitempty"`
	SupportedAgentKinds []SupportedAgentKind `json:"supported_agent_kinds,omitempty"`
}
