package proto

// This package lives at the repo-root module so both the server-side
// gateway/connector AND apps/parsar-daemon can import it. That rules out
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

	// TypePermissionRequest carries an agent's request for human
	// approval. Envelope.ID is the RunID; payload.request_id identifies the decision.
	TypePermissionRequest = "permission_request"

	// TypePermissionCancel signals the agent withdrew an earlier
	// permission request (e.g. its internal timeout fired). Used by
	// the gateway to unblock pending SubmitPermission calls.
	TypePermissionCancel = "permission_cancel"

	// TypePromptForUserChoice asks the human to pick one (or more)
	// answers from a closed list before the agent can continue. Used
	// to intercept Claude Code's built-in AskUserQuestion tool so the
	// daemon doesn't deadlock waiting for a tool_result no one will
	// send. Envelope.ID is the RunID; payload.ask_id identifies the decision.
	TypePromptForUserChoice = "prompt_for_user_choice"

	// TypeInteractionDecisionAck confirms that the daemon-side agent
	// accepted (or definitively rejected) a permission/user-input decision or cancellation.
	// The server must not mark the durable interaction terminal before this
	// frame arrives.
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

// PermissionRequestPayload carries an agent's request for human
// approval. RequestID is the daemon-minted handle used to route a later
// decision. It lives in the payload because Envelope.ID is the run ID used
// by the server gateway to deliver the request to the active run subscriber.
type PermissionRequestPayload struct {
	RequestID string         `json:"request_id"`
	Tool      string         `json:"tool"`
	Title     string         `json:"title"`
	Detail    string         `json:"detail,omitempty"`
	Payload   map[string]any `json:"payload,omitempty"`
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

// PromptForUserChoiceOption is one button / checkbox the user can
// pick when answering a PromptForUserChoice. Label is the human-
// readable choice; Description is optional inline help.
type PromptForUserChoiceOption struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

// PromptForUserChoiceQuestion is one question in a (possibly multi-
// question) AskUserQuestion call. Mirrors the Claude Code built-in
// schema verbatim so the daemon doesn't translate the shape twice.
type PromptForUserChoiceQuestion struct {
	ID          string                      `json:"id"`
	Header      string                      `json:"header,omitempty"`
	Question    string                      `json:"question"`
	MultiSelect bool                        `json:"multi_select,omitempty"`
	IsOther     bool                        `json:"is_other,omitempty"`
	IsSecret    bool                        `json:"is_secret,omitempty"`
	Options     []PromptForUserChoiceOption `json:"options"`
}

// PromptForUserChoicePayload carries the AskUserQuestion interception.
//
// AskID is the daemon-minted "ask_<8hex>" handle the server uses to
// route SubmitPromptForUserChoice back to the right session. It rides
// on the payload (not Envelope.ID) because Envelope.ID is reserved for
// the run id — that's the field server-side session.dispatch fans on
// to deliver the frame to the run's subscriber channel. ToolUseID is
// the originating Claude Code tool_use id; empty when the call came
// through the control_request channel (CCRequestID then identifies the
// daemon-side waiter instead but it doesn't ride on the wire).
type PromptForUserChoicePayload struct {
	AskID            string                        `json:"ask_id"`
	Questions        []PromptForUserChoiceQuestion `json:"questions"`
	ToolUseID        string                        `json:"tool_use_id,omitempty"`
	AutoResolutionMs *uint64                       `json:"auto_resolution_ms,omitempty"`
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
// do inside one prompt session. Runtime-level capabilities such as
// cancellation belong to the daemon connector itself; these bits are
// the engine-specific surface the UI uses for filtering and copy.
type AgentKindCapabilities struct {
	SubagentObservations  bool `json:"subagent_observations,omitempty"`
	Streaming             bool `json:"streaming,omitempty"`
	Permissions           bool `json:"permissions,omitempty"`
	Usage                 bool `json:"usage,omitempty"`
	Resume                bool `json:"resume,omitempty"`
	NativeSessionRecovery bool `json:"native_session_recovery,omitempty"`
	WorkspaceAuthoring    bool `json:"workspace_authoring,omitempty"`
	Steering              bool `json:"steering,omitempty"`
	MessageItems          bool `json:"message_items,omitempty"`

	ToolObservations               bool `json:"tool_observations,omitempty"`
	EnvironmentNone                bool `json:"environment_none,omitempty"`
	LocalEnvironment               bool `json:"local_environment,omitempty"`
	Preparation                    bool `json:"preparation,omitempty"`
	WorkspaceReadPreparation       bool `json:"workspace_read_preparation,omitempty"`
	WorkspaceOutputExport          bool `json:"workspace_output_export,omitempty"`
	ProgrammaticToolCallingDisable bool `json:"programmatic_tool_calling_disable,omitempty"`
	WebSearchControl               bool `json:"web_search_control,omitempty"`
	// ExecutionControls supports typed search and verbosity controls.
	ExecutionControls    bool `json:"execution_controls,omitempty"`
	TextVerbosity        bool `json:"text_verbosity,omitempty"`
	StructuredOutput     bool `json:"structured_output,omitempty"`
	ToolSearch           bool `json:"tool_search,omitempty"`
	MessageImages        bool `json:"message_images,omitempty"`
	FunctionResultImages bool `json:"function_result_images,omitempty"`
	SubagentControl      bool `json:"subagent_control,omitempty"`
	DurableInputReceipts bool `json:"durable_input_receipts,omitempty"`
	// DurableTurns includes strict resume, completion release and cancellation snapshots.
	DurableTurns      bool `json:"durable_turns,omitempty"`
	FunctionTools     bool `json:"function_tools,omitempty"`
	MCPHTTPTools      bool `json:"mcp_http_tools,omitempty"`
	MCPHTTPRequired   bool `json:"mcp_http_required,omitempty"`
	MCPHTTPBearerAuth bool `json:"mcp_http_bearer_auth,omitempty"`
}

// SupportedAgentKind is one daemon-advertised agent engine. Daemons
// can report unavailable kinds with Available=false when the adapter
// exists but the underlying CLI binary is missing.
type SupportedAgentKind struct {
	Kind         string                `json:"kind"`
	Available    bool                  `json:"available"`
	Version      string                `json:"version,omitempty"`
	Capabilities AgentKindCapabilities `json:"capabilities,omitempty"`
}

// HeartbeatPayload advertises only explicit engine descriptors. Missing
// supported_agent_kinds establishes no engine availability or capabilities.
type HeartbeatPayload struct {
	Timestamp           int64                `json:"ts"`
	ActiveRequests      int                  `json:"active_requests"`
	DaemonVersion       string               `json:"daemon_version,omitempty"`
	SupportedAgentKinds []SupportedAgentKind `json:"supported_agent_kinds,omitempty"`
}
