package mcode

import (
	"fmt"
	"os"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

func validateExecutionRequest(req proto.PromptRequestPayload) error {
	if !req.DisableExecutionEnvironment || req.LocalEnvironment != nil || req.RequireExistingNativeSession || req.ExecutionControls == nil {
		return fmt.Errorf("mcode: unsupported execution configuration")
	}
	if !req.DisableSubagents && (req.MaxConcurrentSubagents == nil || *req.MaxConcurrentSubagents < 1) {
		return fmt.Errorf("mcode: Subagent concurrency limit is required")
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
