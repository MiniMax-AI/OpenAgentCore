package claudesdk

import (
	"fmt"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

// Config locates the installed bridge: the Node executable and the bridge's
// absolute entrypoint. Env adds to the runtime check's environment, as the
// native installer's check sets it.
type Config struct {
	Node       string
	Entrypoint string
	Env        []string
}

type subagentOptions struct {
	MaxConcurrent int `json:"max_concurrent"`
}

type startRequest struct {
	NativeModelOptions *nativeModelOptions  `json:"native_model_options,omitempty"`
	ToolSearch         bool                 `json:"tool_search,omitempty"`
	Subagents          *subagentOptions     `json:"subagents,omitempty"`
	OutputFormat       *proto.OutputFormat  `json:"output_format,omitempty"`
	Type               string               `json:"type"`
	Model              string               `json:"model"`
	SystemPrompt       string               `json:"system_prompt"`
	Cwd                string               `json:"cwd"`
	Resume             string               `json:"resume,omitempty"`
	Functions          []proto.FunctionTool `json:"functions,omitempty"`
	MCPHTTPServers     *[]mcpHTTPServer     `json:"mcp_http_servers,omitempty"`
	Workspace          *workspaceProfile    `json:"workspace,omitempty"`
	RequireHistory     bool                 `json:"require_history,omitempty"`
}

// prepareOptions renders the request's execution configuration and the
// selected model provider. The registered factory already admitted the
// selection against the declaration.
func prepareOptions(req agent.PrepareRequest) (startRequest, []string, error) {
	start := startRequest{Type: "start", Resume: req.AgentSessionID, RequireHistory: req.RequireExistingNativeSession, ToolSearch: req.ToolSearch}
	fail := func(reason string) (startRequest, []string, error) {
		return startRequest{}, nil, fmt.Errorf("claudesdk: %s", reason)
	}
	modelConfiguration := req.Prepared
	start.NativeModelOptions = compileNativeModelOptions(modelConfiguration.HarnessConfig)
	if err := validateMCP(req); err != nil {
		return startRequest{}, nil, err
	}
	// The declaration admits only medium text verbosity, the SDK's default text
	// generation, and the fixed native tool profile excludes search.
	if req.ExecutionControls != nil && req.ExecutionControls.OutputFormat != nil {
		if req.ExecutionControls.OutputFormat.Type != "json_schema" {
			return fail("structured output requires a json_schema format")
		}
		start.OutputFormat = req.ExecutionControls.OutputFormat
	}
	if req.ObserveSubagentIdentities {
		if req.DisableSubagents {
			return fail("subagent observation requires enabled subagents")
		}
		limit := 6
		if req.MaxConcurrentSubagents != nil {
			limit = *req.MaxConcurrentSubagents
		}
		if limit < 1 {
			return fail("invalid concurrent subagent limit")
		}
		start.Subagents = &subagentOptions{MaxConcurrent: limit}
	}
	functions, err := functionTools(req.FunctionTools)
	if err != nil {
		return startRequest{}, nil, err
	}
	start.Functions = functions
	start.Model, start.SystemPrompt = modelConfiguration.Model, req.SystemPrompt
	// The key renders as ANTHROPIC_API_KEY, which Claude Code sends as the
	// X-Api-Key header that the anthropic protocol declares.
	selected := modelConfiguration.Provider
	return start, []string{"ANTHROPIC_BASE_URL=" + selected.BaseURL, "ANTHROPIC_API_KEY=" + selected.APIKey}, nil
}
