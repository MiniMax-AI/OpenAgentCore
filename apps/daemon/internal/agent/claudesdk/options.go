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
	Input              proto.MessageInput   `json:"input,omitempty"`
	Model              string               `json:"model"`
	SystemPrompt       string               `json:"system_prompt"`
	Cwd                string               `json:"cwd"`
	Resume             string               `json:"resume,omitempty"`
	ObserveMessages    bool                 `json:"observe_messages,omitempty"`
	Functions          []proto.FunctionTool `json:"functions,omitempty"`
	MCPHTTPServers     *[]mcpHTTPServer     `json:"mcp_http_servers,omitempty"`
	Workspace          *workspaceProfile    `json:"workspace,omitempty"`
	RequireHistory     bool                 `json:"require_history,omitempty"`
	observeFunctions   bool
}

func prepare(config Config, req proto.PromptRequestPayload) (startRequest, []string, error) {
	if req.RunID == "" || req.Input.Validate() != nil {
		return startRequest{}, nil, fmt.Errorf("claudesdk: run id and prompt are required")
	}
	start, env, err := prepareConfiguration(config, req)
	if err != nil {
		return startRequest{}, nil, err
	}
	start.Input = req.Input
	return start, env, nil
}

func prepareConfiguration(config Config, req proto.PromptRequestPayload) (startRequest, []string, error) {
	start := startRequest{Type: "start", Resume: req.AgentSessionID, RequireHistory: req.RequireExistingNativeSession, ObserveMessages: req.ObserveMessages, Functions: req.FunctionTools, observeFunctions: req.ObserveToolObservations}
	fail := func(reason string) (startRequest, []string, error) {
		return startRequest{}, nil, fmt.Errorf("claudesdk: %s", reason)
	}
	modelConfiguration, err := harnessconfiguration.Configuration().Prepare(req.AgentOptions)
	if err != nil {
		return startRequest{}, nil, err
	}
	start.NativeModelOptions = compileNativeModelOptions(modelConfiguration.HarnessConfig)
	if req.WorkspaceAuthoring {
		return fail("requested capability is not available in the private SDK adapter")
	}
	if err := req.ValidateToolSearch(true); err != nil {
		return startRequest{}, nil, err
	}
	if req.ToolSearch {
		if (req.LocalEnvironment != nil && (len(req.LocalEnvironment.Skills) != 0 || len(req.LocalEnvironment.MCP) != 0)) || req.MCPHTTPServers != nil || !req.DisableSubagents || (req.ExecutionControls != nil && req.ExecutionControls.OutputFormat != nil) {
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
		if format.Type != "json_schema" || !req.ObserveMessages || !req.DisableSubagents || req.MCPHTTPServers != nil || (req.LocalEnvironment != nil && (len(req.LocalEnvironment.MCP) != 0 || len(req.LocalEnvironment.Skills) != 0)) {
			return fail("structured output requires the qualified message-observing single-agent function profile")
		}
		if err := proto.ValidateBinary64Schema(format.Schema); err != nil {
			return startRequest{}, nil, err
		}
		start.OutputFormat = format
	}
	if req.ObserveSubagentIdentities {
		if req.DisableSubagents || len(req.FunctionTools) != 0 || req.MCPHTTPServers != nil || (req.LocalEnvironment != nil && len(req.LocalEnvironment.MCP) != 0) {
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
	var provider []string
	for name, raw := range req.AgentOptions {
		if name == "harness_config" {
			continue
		}
		if name == "model_provider" {
			var err error
			provider, err = providerEnvironment(raw)
			if err != nil {
				return startRequest{}, nil, err
			}
			continue
		}
		if name == "system_prompt" && raw == nil {
			continue
		}
		value, ok := raw.(string)
		if !ok {
			return fail("model and system_prompt options must be strings")
		}
		switch name {
		case "model":
			start.Model = value
		case "system_prompt":
			start.SystemPrompt = value
		default:
			return fail("unsupported option: " + name)
		}
	}
	if strings.TrimSpace(start.Model) == "" {
		return fail("model is required")
	}
	if !filepath.IsAbs(config.Entrypoint) {
		return fail("SDK entrypoint must be absolute")
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
		return fail("local execution and history recovery require a dedicated workspace")
	}
	root, err := paths.Root()
	if err != nil {
		return startRequest{}, nil, err
	}
	relative, err := filepath.Rel(root, config.StateDir)
	if err != nil || !filepath.IsAbs(root) || !filepath.IsAbs(config.StateDir) || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fail("SDK state must be in a managed runtime subdirectory")
	}
	start.Cwd = req.WorkDir
	if start.Cwd == "" {
		start.Cwd = filepath.Join(config.StateDir, "work")
	}
	if strings.HasPrefix(start.Cwd, "~/") {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return startRequest{}, nil, err
		}
		start.Cwd = filepath.Join(homeDir, strings.TrimPrefix(start.Cwd, "~/"))
	}
	if !filepath.IsAbs(start.Cwd) {
		return fail("work_dir must be absolute or start with ~/")
	}
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
