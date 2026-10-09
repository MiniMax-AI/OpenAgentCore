// Package codex is the agent_kind="codex" adapter. It drives the
// OpenAI Codex CLI (codex-rs / @openai/codex) via `codex app-server --stdio`,
// speaking the JSON-RPC 2.0 protocol that the app-server exposes over
// stdio.
//
// Codex uses an app-server transport:
//
//  1. The wire protocol is JSON-RPC (request / response / notification /
//     server-request) rather than NDJSON event-stream. See rpc.go.
//
//  2. Multi-turn context lives in a Codex "thread" identified by the
//     thread_id returned in the first `thread/started` notification.
//     The daemon stamps that id into DonePayload.Metadata["agent_session_id"]
//     so the connector's RememberSession path persists it.
//     Subsequent turns spawn a fresh app-server and call `thread/resume`
//     with that id to graft the prior turn's context back in.
package codex

import "encoding/json"

import "fmt"

// JsonRpcVersion is the JSON-RPC 2.0 marker carried on every outbound
// frame. Inbound frames omit the field per Codex's app-server convention,
// so the parser does not enforce it on responses.
const JsonRpcVersion = "2.0"

// ---------------------------------------------------------------------------
// JSON-RPC envelopes (over stdio NDJSON)
// ---------------------------------------------------------------------------

// JsonRpcRequest is an outbound RPC call (client → codex). id is a
// daemon-minted 16-hex string; an empty id marks a notification (no
// response expected).
type JsonRpcRequest struct {
	JsonRpc string `json:"jsonrpc"`
	ID      string `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

// JsonRpcResponse is an outbound reply to a server-initiated request.
// Either Result or Error must be non-nil.
type JsonRpcResponse struct {
	JsonRpc string        `json:"jsonrpc"`
	ID      any           `json:"id"`
	Result  any           `json:"result,omitempty"`
	Error   *JsonRpcError `json:"error,omitempty"`
}

// JsonRpcError carries a structured failure reply. Codes follow the
// JSON-RPC 2.0 spec (-32601 method-not-found, -32603 internal error,
// etc.).
type JsonRpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func (e *JsonRpcError) Error() string { return fmt.Sprintf("%d %s", e.Code, e.Message) }

// ---------------------------------------------------------------------------
// initialize handshake
// ---------------------------------------------------------------------------

// InitializeCapabilities mirrors codex-rs/app-server/src/protocol.rs.
// experimentalApi=true opts into the experimental thread/* fields and
// notification stream.
type InitializeCapabilities struct {
	ExperimentalAPI    bool      `json:"experimentalApi"`
	RequestAttestation bool      `json:"requestAttestation"`
	OptOutMethods      *[]string `json:"optOutNotificationMethods,omitempty"`
}

type InitializeClientInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type InitializeParams struct {
	ClientInfo   InitializeClientInfo    `json:"clientInfo"`
	Capabilities *InitializeCapabilities `json:"capabilities"`
}

type InitializeResult struct {
	MCPServerInvalidation bool   `json:"mcpServerInvalidation"`
	UserAgent             string `json:"userAgent"`
	CodexHome             string `json:"codexHome"`
	PlatformFamily        string `json:"platformFamily,omitempty"`
	PlatformOs            string `json:"platformOs,omitempty"`
}

// The selected stdio clients are invalidated in the entire owned thread subtree.
// The response confirms local invalidation, not remote completion or readiness.
type McpServerInvalidateParams struct {
	ThreadID    string   `json:"threadId"`
	ServerNames []string `json:"serverNames"`
}

type McpServerInvalidateResponse struct {
	ServerNames []string `json:"serverNames"`
}

type SkillsExtraRootsSetParams struct {
	ExtraRoots []string `json:"extraRoots"`
}

type SkillsListParams struct {
	Cwds        []string `json:"cwds"`
	ForceReload bool     `json:"forceReload"`
}

// SkillsListResponse holds the fields of skills/list that the adapter checks.
type SkillsListResponse struct {
	Data []struct {
		Skills []struct {
			Path         string `json:"path"`
			Dependencies any    `json:"dependencies"`
		} `json:"skills"`
		Errors []struct {
			Path string `json:"path"`
		} `json:"errors"`
	} `json:"data"`
}

// ---------------------------------------------------------------------------
// thread/start, thread/resume, thread/list
// ---------------------------------------------------------------------------

type ThreadStartParams struct {
	HistoryMode    string `json:"historyMode,omitempty"`
	Cwd            string `json:"cwd"`
	Model          string `json:"model,omitempty"`
	ModelProvider  string `json:"modelProvider,omitempty"`
	ApprovalPolicy string `json:"approvalPolicy"`
	// Sandbox is the kebab-case mode string. Codex silently falls back to
	// read-only for the legacy sandboxPolicy object.
	Sandbox               string                `json:"sandbox"`
	DeveloperInstructions string                `json:"developerInstructions,omitempty"`
	RuntimeWorkspaceRoots []string              `json:"runtimeWorkspaceRoots,omitempty"`
	DynamicTools          []dynamicFunctionTool `json:"dynamicTools,omitempty"`
}

type Thread struct {
	ID         string `json:"id"`
	Cwd        string `json:"cwd,omitempty"`
	Preview    string `json:"preview,omitempty"`
	UpdatedAt  int64  `json:"updatedAt,omitempty"`
	CreatedAt  int64  `json:"createdAt,omitempty"`
	Status     any    `json:"status,omitempty"`
	Path       string `json:"path,omitempty"`
	CLIVersion string `json:"cliVersion,omitempty"`
}

type ThreadStartResult struct {
	Thread Thread `json:"thread"`
	Model  string `json:"model,omitempty"`
}

type ThreadResumeParams struct {
	Cwd                   string `json:"cwd,omitempty"`
	DeveloperInstructions string `json:"developerInstructions"`
	ThreadID              string `json:"threadId"`
	ApprovalPolicy        string `json:"approvalPolicy"`
	Sandbox               string `json:"sandbox"`
}

// ---------------------------------------------------------------------------
// turn/start, turn/interrupt
// ---------------------------------------------------------------------------

// UserInputType discriminates a UserInput element. Codex accepts mixed
// arrays of text + image entries on a single turn.
type UserInputType string

const (
	UserInputText       UserInputType = "text"
	UserInputLocalImage UserInputType = "localImage"
	UserInputRemoteImg  UserInputType = "image"
)

// UserInput is one element of TurnStartParams.Input. Each variant uses a
// distinct field set; producers must populate only the fields that
// match Type.
type UserInput struct {
	Type UserInputType `json:"type"`
	// Text variant
	Text         string `json:"text,omitempty"`
	TextElements []any  `json:"text_elements,omitempty"`
	// LocalImage variant
	Path string `json:"path,omitempty"`
	// Image (remote URL) variant
	URL string `json:"url,omitempty"`
}

type TurnStartParams struct {
	ThreadID          string             `json:"threadId"`
	Input             []UserInput        `json:"input"`
	CollaborationMode *CollaborationMode `json:"collaborationMode,omitempty"`
}

type CollaborationModeKind string

const CollaborationModeDefault CollaborationModeKind = "default"

type CollaborationMode struct {
	Mode     CollaborationModeKind     `json:"mode"`
	Settings CollaborationModeSettings `json:"settings"`
}

type CollaborationModeSettings struct {
	ReasoningEffort       string  `json:"reasoning_effort,omitempty"`
	Model                 string  `json:"model"`
	DeveloperInstructions *string `json:"developer_instructions"`
}

type TurnInterruptParams struct {
	ThreadID string `json:"threadId"`
	TurnID   string `json:"turnId"`
}

type TurnUsage struct {
	observed              bool
	complete              bool
	ReasoningOutputTokens int `json:"reasoningOutputTokens,omitempty"`
	InputTokens           int `json:"inputTokens,omitempty"`
	OutputTokens          int `json:"outputTokens,omitempty"`
	CachedInputTokens     int `json:"cachedInputTokens,omitempty"`
	CacheReadInputTokens  int `json:"cacheReadInputTokens,omitempty"`
	TotalTokens           int `json:"totalTokens,omitempty"`
}

type Turn struct {
	CompletedAt *int64     `json:"completedAt,omitempty"`
	ID          string     `json:"id"`
	Usage       *TurnUsage `json:"usage,omitempty"`
	Status      string     `json:"status,omitempty"`
	// Failed Turns carry the native provider error here.
	Error *TurnError `json:"error,omitempty"`
}

// CodexErrorInfo is a native enum with string and object variants.
type TurnError struct {
	Message           string          `json:"message,omitempty"`
	CodexErrorInfo    json.RawMessage `json:"codexErrorInfo,omitempty"`
	AdditionalDetails *string         `json:"additionalDetails,omitempty"`
}

// ---------------------------------------------------------------------------
// ThreadItem (the variants we map; the rest fall through to a generic
// catch-all so codex upgrades that add new item kinds do not break
// parsing).
// ---------------------------------------------------------------------------

// ThreadItem is decoded loosely: parser keeps the raw JSON around so
// future fields can be inspected via a second unmarshal without round-
// tripping every variant through a hand-written struct.
type ThreadItem struct {
	Type string `json:"type"`
	ID   string `json:"id,omitempty"`

	// agentMessage / reasoning
	Phase       string   `json:"phase,omitempty"`
	Text        string   `json:"text,omitempty"`
	Summary     []string `json:"summary,omitempty"`
	Content     []string `json:"content,omitempty"`
	SummaryText string   `json:"summary_text,omitempty"`

	// commandExecution
	Command  string `json:"command,omitempty"`
	Cwd      string `json:"cwd,omitempty"`
	ExitCode *int   `json:"exitCode,omitempty"`
	Status   string `json:"status,omitempty"`

	// fileChange
	Changes []map[string]any `json:"changes,omitempty"`

	// mcpToolCall
	Server    string `json:"server,omitempty"`
	Tool      string `json:"tool,omitempty"`
	Arguments any    `json:"arguments,omitempty"`

	// dynamicToolCall
	Namespace string `json:"namespace,omitempty"`

	// webSearch
	Query string `json:"query,omitempty"`
}

// ---------------------------------------------------------------------------
// Notification params we subscribe to (param shapes only — method names
// live as constants in session.go's handler registration).
// ---------------------------------------------------------------------------

type ThreadStartedNotification struct {
	Thread Thread `json:"thread"`
}

type TurnStartedNotification struct {
	ThreadID string `json:"threadId"`
	Turn     Turn   `json:"turn"`
}

type TurnCompletedNotification struct {
	ThreadID string `json:"threadId"`
	Turn     Turn   `json:"turn"`
}

type ItemStartedNotification struct {
	ThreadID    string     `json:"threadId"`
	TurnID      string     `json:"turnId"`
	Item        ThreadItem `json:"item"`
	StartedAtMs int64      `json:"startedAtMs,omitempty"`
}

type ItemCompletedNotification struct {
	ThreadID      string     `json:"threadId"`
	TurnID        string     `json:"turnId"`
	Item          ThreadItem `json:"item"`
	CompletedAtMs int64      `json:"completedAtMs,omitempty"`
}

type AgentMessageDeltaNotification struct {
	ThreadID string `json:"threadId"`
	TurnID   string `json:"turnId"`
	ItemID   string `json:"itemId"`
	Delta    string `json:"delta"`
}

type ReasoningDeltaNotification = AgentMessageDeltaNotification

type ThreadTokenUsageUpdatedNotification struct {
	ThreadID   string     `json:"threadId"`
	TurnID     string     `json:"turnId"`
	Usage      *TurnUsage `json:"usage,omitempty"` // Legacy turn-level payload.
	TokenUsage *struct {
		Total *TurnUsage `json:"total"`
	} `json:"tokenUsage,omitempty"`
}

type ErrorNotification struct {
	ThreadID string     `json:"threadId"`
	TurnID   string     `json:"turnId"`
	Error    *TurnError `json:"error,omitempty"`
	Message  string     `json:"message,omitempty"`
}
