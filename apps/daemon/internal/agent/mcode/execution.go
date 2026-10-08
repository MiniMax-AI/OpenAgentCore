package mcode

import (
	"fmt"
	"os"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

func validateExecutionRequest(req proto.PromptRequestPayload) error {
	if !req.DisableExecutionEnvironment || req.AgentStateKey == "" || req.LocalEnvironment != nil || req.RequireExistingNativeSession || len(req.FunctionTools) != 0 || (req.MCPHTTPServers != nil && len(*req.MCPHTTPServers) != 0) {
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

// ACP commands are only recognized for a single text block. A second, empty
// block keeps public input as user text.
func promptContent(text string) []map[string]string {
	return []map[string]string{{"type": "text", "text": text}, {"type": "text", "text": ""}}
}
