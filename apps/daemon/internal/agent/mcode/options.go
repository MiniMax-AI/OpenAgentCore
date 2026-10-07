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
}

func prepareOptions(req proto.PromptRequestPayload) (launchOptions, error) {
	var result launchOptions
	if _, err := harnessconfiguration.Configuration().PrepareHarnessConfig(req.AgentOptions); err != nil {
		return result, err
	}
	if err := validateExecutionRequest(req); err != nil {
		return result, err
	}
	dataDir, err := agent.StateDir("mcode", req.AgentStateKey)
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
	config := map[string]any{"logLevel": "error", "skills": map[string]any{"external": map[string]any{"enabled": false}}}
	result.Model = optionString(opts, "model")
	if result.Model == "" {
		return result, fmt.Errorf("mcode: model is required")
	}
	provider, err := modelProviderConfig(opts["model_provider"], result.Model)
	if err != nil {
		return result, err
	}
	config["custom_provider"] = map[string]any{"oac": provider}
	configureTextExecution(config)
	if !req.DisableSubagents {
		config["agents"] = map[string]any{"default": map[string]any{
			"tools":        []string{"task", "task_append", "task_query", "task_output", "task_stop"},
			"builtinTools": []string{"task", "task_append", "task_query", "task_output", "task_stop"}, "skills": []string{},
			"features": map[string]bool{"mavis": false, "delegation": true, "webSearch": false},
		}}
	}
	config["permissionMode"] = "auto"
	data, err := json.Marshal(config)
	if err != nil {
		return result, err
	}
	if err := os.WriteFile(filepath.Join(result.DataDir, "config.yaml"), data, 0o600); err != nil {
		return result, err
	}
	result.Env = append(executionEnvironment(), "OAC_RUNTIME_MCODE_TOOL_POLICY=protected-mcp-v1")
	if err := os.WriteFile(filepath.Join(result.DataDir, "mcp.json"), []byte(`{"mcpServers":{}}`), 0o600); err != nil {
		return result, err
	}
	if !req.DisableSubagents {
		result.Env = append(result.Env, "OAC_RUNTIME_MCODE_MAX_SUBAGENTS="+strconv.Itoa(*req.MaxConcurrentSubagents))
	} else {
		result.Env = append(result.Env, "OAC_RUNTIME_MCODE_MAX_SUBAGENTS=0")
	}
	// The adapter owns the native state location, including after cold resume.
	result.Env = append(result.Env, "MINIMAX_DATA_DIR="+result.DataDir, "HOME="+result.DataDir, "USERPROFILE="+result.DataDir)
	result.MCP = []map[string]any{}
	return result, nil
}

func optionString(options map[string]any, key string) string {
	value, _ := options[key].(string)
	return value
}
