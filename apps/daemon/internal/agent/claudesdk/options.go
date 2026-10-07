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
	Functions          []proto.FunctionTool `json:"functions,omitempty"`
	MCPHTTPServers     *[]mcpHTTPServer     `json:"mcp_http_servers,omitempty"`
	Workspace          *workspaceProfile    `json:"workspace,omitempty"`
	RequireHistory     bool                 `json:"require_history,omitempty"`
}

func prepareConfiguration(config Config, req proto.PromptRequestPayload) (startRequest, []string, error) {
	start, provider, err := prepareOptions(req)
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

// prepareOptions renders the request's execution configuration and the
// selected model provider. The registered factory already admitted the
// selection against the declaration.
func prepareOptions(req proto.PromptRequestPayload) (startRequest, []string, error) {
	start := startRequest{Type: "start", Resume: req.AgentSessionID, RequireHistory: req.RequireExistingNativeSession, ToolSearch: req.ToolSearch}
	fail := func(reason string) (startRequest, []string, error) {
		return startRequest{}, nil, fmt.Errorf("claudesdk: %s", reason)
	}
	modelConfiguration, err := harnessconfiguration.Configuration().Prepare(req)
	if err != nil {
		return startRequest{}, nil, err
	}
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
	if start.Functions, err = functionTools(req.FunctionTools); err != nil {
		return startRequest{}, nil, err
	}
	start.Model, start.SystemPrompt = modelConfiguration.Model, req.SystemPrompt
	// The key renders as ANTHROPIC_API_KEY, which Claude Code sends as the
	// X-Api-Key header that the anthropic protocol declares.
	selected := modelConfiguration.Provider
	return start, []string{"ANTHROPIC_BASE_URL=" + selected.BaseURL, "ANTHROPIC_API_KEY=" + selected.APIKey}, nil
}
