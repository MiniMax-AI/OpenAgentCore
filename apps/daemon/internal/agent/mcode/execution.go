package mcode

import (
	"fmt"
	"os"
	"strings"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	declaration "github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig/mcode"
)

func validateExecutionRequest(req proto.PromptRequestPayload) error {
	if !req.DisableExecutionEnvironment || req.AgentStateKey == "" || req.LocalEnvironment != nil || req.RequireExistingNativeSession {
		return fmt.Errorf("mcode: unsupported execution configuration")
	}
	if req.ExecutionControls == nil || req.ExecutionControls.OutputFormat != nil {
		return fmt.Errorf("mcode: unsupported execution controls")
	}
	if err := declaration.Declaration.ValidateExecutionControls(req.ExecutionControls); err != nil {
		return err
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

// Harness children use the daemon user's ordinary environment.
func executionEnvironment() []string { return os.Environ() }

// ACP joins text blocks with "\n\n" and parses slash commands only from a
// single block. A second, empty block keeps input that starts with "/" as user
// text; any other input is sent as one block so the model sees it unchanged.
func promptContent(text string) []map[string]string {
	content := []map[string]string{{"type": "text", "text": text}}
	if strings.HasPrefix(text, "/") {
		content = append(content, map[string]string{"type": "text", "text": ""})
	}
	return content
}
