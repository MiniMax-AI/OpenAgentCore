package mcode

import (
	"fmt"
	"os"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

// SupportsExecution is an operator opt-in, separate from ordinary product availability.
func SupportsExecution(version string) bool {
	return os.Getenv("OAC_RUNTIME_MCODE_AGENTS_API") == "1" && version == SupportedVersion
}

func validateExecutionRequest(req proto.PromptRequestPayload) error {
	if !req.DisableExecutionEnvironment || req.WorkDir != "" || req.AgentStateKey == "" || req.LocalEnvironment != nil || req.RequireExistingNativeSession || len(req.FunctionTools) != 0 || (req.MCPHTTPServers != nil && len(*req.MCPHTTPServers) != 0) {
		return fmt.Errorf("mcode: unsupported execution configuration")
	}
	if req.ExecutionControls == nil || req.ExecutionControls.OutputFormat != nil || req.ExecutionControls.WebSearch != "disabled" || (req.ExecutionControls.TextVerbosity != "" && req.ExecutionControls.TextVerbosity != "medium") {
		return fmt.Errorf("mcode: unsupported execution controls")
	}
	if !req.DisableSubagents {
		if req.MaxConcurrentSubagents == nil || *req.MaxConcurrentSubagents < 1 {
			return fmt.Errorf("mcode: Subagent concurrency limit is required")
		}
		if _, _, err := subagentReader(); err != nil {
			return err
		}
	}
	if mode := optionString(req.AgentOptions, "mode"); mode != "" {
		return fmt.Errorf("mcode: text execution uses default native permissions")
	}
	if req.AgentOptions["plugins"] != nil {
		return fmt.Errorf("mcode: execution cannot import plugins")
	}
	if req.AgentOptions["skills"] != nil || req.AgentOptions["mcp_servers"] != nil || req.AgentOptions["env"] != nil {
		return fmt.Errorf("mcode: execution cannot import product capabilities or environment")
	}
	return nil
}

func configureTextExecution(config map[string]any) {
	config["agents"] = map[string]any{"default": map[string]any{
		"tools": []string{}, "builtinTools": []string{}, "skills": []string{},
		"features": map[string]bool{"mavis": false, "delegation": false, "webSearch": false},
	}}
	config["askUser"] = map[string]bool{"enabled": false}
	config["beta"] = map[string]bool{"browserUseTooling": false, "mcodeTools": false, "threadGoal": false}
}

// Harness children use the daemon user's ordinary environment.
func executionEnvironment() []string { return os.Environ() }

// ACP commands are only recognized for a single text block. Public input must
// remain user text; ordinary product Sessions retain their native command behavior.
func promptContent(text string, public bool) []map[string]string {
	blocks := []map[string]string{{"type": "text", "text": text}}
	if public {
		blocks = append(blocks, map[string]string{"type": "text", "text": ""})
	}
	return blocks
}
