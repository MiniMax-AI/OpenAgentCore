package mcode

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	harnessconfiguration "github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig/mcode"
)

type launchOptions struct {
	Dir, DataDir, Model string
	Env                 []string
	MCP                 []map[string]any
	Functions           *functionServer
}

// workspaceProfile selects the native tools of a Session that has an Environment.
type workspaceProfile struct {
	// ToolEnv is the Runtime's frozen tool environment as KEY=value entries.
	ToolEnv []string
	// WebFetch keeps the native web_fetch tool.
	WebFetch bool
}

// prepareOptions writes the native configuration. A nil workspace is the none
// placement: the native process has no builtin file, process or fetch tools.
func prepareOptions(runtimeRoot string, req proto.PromptRequestPayload, workspace *workspaceProfile) (launchOptions, error) {
	var result launchOptions
	if _, err := harnessconfiguration.Declaration.Configuration.PrepareHarnessConfig(req.AgentOptions); err != nil {
		return result, err
	}
	if err := validateExecutionRequest(req); err != nil {
		return result, err
	}
	dataDir, err := agent.StateDir(runtimeRoot, "mcode", req.AgentStateKey)
	if err != nil {
		return result, err
	}
	result.DataDir = dataDir
	result.Dir = filepath.Join(result.DataDir, "workspace")
	if err := os.MkdirAll(result.DataDir, 0o700); err != nil {
		return result, err
	}
	if err := os.MkdirAll(result.Dir, 0o700); err != nil {
		return result, err
	}
	opts := req.AgentOptions
	prompt := optionString(opts, "system_prompt")
	if len(prompt) > 32*1024 {
		return result, fmt.Errorf("mcode: combined instructions exceed the CLI's 32 KiB limit")
	}
	if err := os.WriteFile(filepath.Join(result.DataDir, "AGENTS.md"), []byte(prompt), 0o600); err != nil {
		return result, err
	}
	config := map[string]any{}
	effort := ""
	if req.ExecutionControls != nil {
		effort = req.ExecutionControls.ReasoningEffort
	}
	result.Model = optionString(opts, "model")
	if result.Model == "" {
		return result, fmt.Errorf("mcode: model is required")
	}
	provider, err := modelProviderConfig(opts["model_provider"], result.Model, effort)
	if err != nil {
		return result, err
	}
	config["custom_provider"] = map[string]any{"oac": provider}
	config["agents"] = map[string]any{"default": agentCapabilities(req, workspace)}
	// Unattended: no ask-user. Native mcode-tools needs a MiniMax account sign-in that Core does not hold.
	// Thread goals schedule continuation Turns after the Turn ends.
	config["askUser"] = map[string]bool{"enabled": false}
	config["beta"] = map[string]bool{"mcodeTools": false, "threadGoal": false}
	// Unattended: tools, including MCP tools on environment none, run without approval prompts.
	config["permissionMode"] = "bypassPermissions"
	data, err := json.Marshal(config)
	if err != nil {
		return result, err
	}
	if err := os.WriteFile(filepath.Join(result.DataDir, "config.yaml"), data, 0o600); err != nil {
		return result, err
	}
	result.Env = executionEnvironment()
	if workspace != nil {
		result.Env = append(result.Env, workspace.ToolEnv...)
	}
	// Background bash outlives the Turn and escapes the process group; Subagent task output stays.
	result.Env = append(result.Env, "OAC_RUNTIME_MCODE_BACKGROUND_BASH=off")
	if !req.DisableSubagents {
		result.Env = append(result.Env, "OAC_RUNTIME_MCODE_MAX_SUBAGENTS="+strconv.Itoa(*req.MaxConcurrentSubagents))
	} else {
		result.Env = append(result.Env, "OAC_RUNTIME_MCODE_MAX_SUBAGENTS=0")
	}
	// The adapter owns the native state location, including after cold resume.
	result.Env = append(result.Env, "MINIMAX_DATA_DIR="+result.DataDir, "HOME="+result.DataDir, "USERPROFILE="+result.DataDir)
	return result, nil
}

// agentCapabilities is the native agents.default ceiling. A native ID list is
// all-or-nothing: one unknown ID drops the whole list and enables every tool.
func agentCapabilities(req proto.PromptRequestPayload, workspace *workspaceProfile) map[string]any {
	tools := []string{}
	if workspace != nil {
		// website_deploy needs a MiniMax account and stays out of the list.
		tools = append(tools, "read", "write", "edit", "bash", "grep", "glob", "todowrite")
		if workspace.WebFetch {
			tools = append(tools, "web_fetch")
		}
	}
	return map[string]any{
		"tools": tools,
		"features": map[string]bool{
			// The mavis tool carries cron scheduling that runs beyond a Turn, plus session send, agent create and MCP create.
			"mavis":      false,
			"delegation": !req.DisableSubagents,
			// Native web search calls MiniMax's cloud with a MiniMax account credential that Core does not hold.
			"webSearch": false,
		},
	}
}

func optionString(options map[string]any, key string) string {
	value, _ := options[key].(string)
	return value
}
