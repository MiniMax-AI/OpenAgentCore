package claudesdk

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/paths"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	harnessconfiguration "github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig/claudesdk"
)

type Config struct {
	Node       string
	Entrypoint string
	StateDir   string
	Env        []string
	Workspace  *WorkspaceConfig
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
	ObserveMessages    bool                 `json:"observe_messages,omitempty"`
	Functions          []proto.FunctionTool `json:"functions,omitempty"`
	MCPHTTPServers     *[]mcpHTTPServer     `json:"mcp_http_servers,omitempty"`
	Workspace          *workspaceProfile    `json:"workspace,omitempty"`
	RequireHistory     bool                 `json:"require_history,omitempty"`
}

func prepareConfiguration(config Config, req proto.PromptRequestPayload) (startRequest, []string, error) {
	start, provider, err := prepareOptions(req, req.MCPHTTPServers != nil || (req.LocalEnvironment != nil && len(req.LocalEnvironment.MCP) != 0))
	if err != nil {
		return startRequest{}, nil, err
	}
	if !filepath.IsAbs(config.Entrypoint) {
		return startRequest{}, nil, fmt.Errorf("claudesdk: SDK entrypoint must be absolute")
	}
	config.Env = withProvider(config.Env, provider)
	if config.Workspace != nil {
		profile, env, err := prepareWorkspace(config, req)
		if err != nil {
			return startRequest{}, nil, err
		}
		start.Workspace = profile
		start.Cwd = workspaceCwd(config.Workspace)
		return start, env, nil
	}
	if req.LocalEnvironment != nil || req.RequireExistingNativeSession {
		return startRequest{}, nil, fmt.Errorf("claudesdk: local execution and history recovery require a dedicated workspace")
	}
	root, err := paths.Root()
	if err != nil {
		return startRequest{}, nil, err
	}
	relative, err := filepath.Rel(root, config.StateDir)
	if err != nil || !filepath.IsAbs(root) || !filepath.IsAbs(config.StateDir) || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return startRequest{}, nil, fmt.Errorf("claudesdk: SDK state must be in a managed runtime subdirectory")
	}
	start.Cwd = filepath.Join(config.StateDir, "work")
	for _, dir := range []string{config.StateDir, filepath.Join(config.StateDir, "tmp"), start.Cwd} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return startRequest{}, nil, err
		}
	}
	env := withProvider(append(append([]string{}, os.Environ()...), config.Env...), provider)
	env = append(env, "CLAUDE_CONFIG_DIR="+config.StateDir, "TMPDIR="+filepath.Join(config.StateDir, "tmp"), "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1")
	projectedMCP, mcpEnv, err := prepareRuntimeMCP(req)
	if err != nil {
		return startRequest{}, nil, err
	}
	if req.MCPHTTPServers != nil {
		servers := make([]mcpHTTPServer, 0, len(projectedMCP))
		for _, server := range projectedMCP {
			servers = append(servers, server.mcpHTTPServer)
		}
		start.MCPHTTPServers = &servers
	}
	env = append(env, mcpEnv...)
	return start, env, nil
}

// prepareOptions validates the request's execution configuration and renders
// the selected model provider. mcp reports whether the Executor serves MCP, which
// an agent-host view takes from its Session rather than the request.
func prepareOptions(req proto.PromptRequestPayload, mcp bool) (startRequest, []string, error) {
	skills := req.LocalEnvironment != nil && len(req.LocalEnvironment.Skills) != 0
	start := startRequest{Type: "start", Resume: req.AgentSessionID, RequireHistory: req.RequireExistingNativeSession, ObserveMessages: req.ObserveMessages, Functions: req.FunctionTools}
	fail := func(reason string) (startRequest, []string, error) {
		return startRequest{}, nil, fmt.Errorf("claudesdk: %s", reason)
	}
	modelConfiguration, err := harnessconfiguration.Configuration().Prepare(req)
	if err != nil {
		return startRequest{}, nil, err
	}
	start.NativeModelOptions = compileNativeModelOptions(modelConfiguration.HarnessConfig)
	if err := req.ValidateToolSearch(true); err != nil {
		return startRequest{}, nil, err
	}
	if req.ToolSearch {
		if skills || mcp || !req.DisableSubagents || (req.ExecutionControls != nil && req.ExecutionControls.OutputFormat != nil) {
			return fail("tool discovery requires the single-agent text/function profile")
		}
		start.ToolSearch = true
	}
	if err := validateMCP(req); err != nil {
		return startRequest{}, nil, err
	}
	// Search is disabled by the fixed native tool profile. Medium selects the
	// SDK's default text generation; it has no native verbosity-level option.
	if controls := req.ExecutionControls; controls != nil && (controls.WebSearch != "disabled" || controls.TextVerbosity != "medium") {
		return fail("execution controls require disabled web search and medium text verbosity")
	}
	if req.ExecutionControls != nil && req.ExecutionControls.OutputFormat != nil {
		format := req.ExecutionControls.OutputFormat
		if format.Type != "json_schema" || !req.ObserveMessages || !req.DisableSubagents || mcp || skills {
			return fail("structured output requires the qualified message-observing single-agent function profile")
		}
		if err := proto.ValidateBinary64Schema(format.Schema); err != nil {
			return startRequest{}, nil, err
		}
		start.OutputFormat = format
	}
	if req.ObserveSubagentIdentities {
		if req.DisableSubagents || len(req.FunctionTools) != 0 || mcp {
			return fail("subagent execution does not support this tool combination")
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
	if err := validateFunctions(req.FunctionTools); err != nil {
		return startRequest{}, nil, err
	}
	start.Model, start.SystemPrompt = modelConfiguration.Model, req.SystemPrompt
	// The key renders as ANTHROPIC_API_KEY, which Claude Code sends as the
	// X-Api-Key header that the anthropic protocol declares.
	selected := modelConfiguration.Provider
	return start, []string{"ANTHROPIC_BASE_URL=" + selected.BaseURL, "ANTHROPIC_API_KEY=" + selected.APIKey}, nil
}
