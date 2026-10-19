package mcode

import (
	"encoding/json"
	"math"
	"strings"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	declaration "github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig/mcode"
)

func (s *Session) emitToolStage(update toolUpdate, stage string) error {
	// Public functions report through the function call, not as MCP observations.
	if update.mcp != nil && update.mcp.server == declaration.FunctionServerLabel {
		if stage == "after" && update.Status != "incomplete" {
			s.confirmFunction(update)
		}
		return nil
	}
	payload := proto.ToolCallPayload{ID: update.ID, Name: update.Name, Stage: stage, Args: update.RawInput}
	if stage == "after" {
		payload.Result = map[string]any{"output": update.RawOutput, "status": update.Status}
	}
	payload.Observation = s.nativeToolObservation(update, stage)
	if payload.Observation == nil {
		var err error
		payload.Observation, err = mcpObservation(update, stage)
		if err != nil {
			return err
		}
	}
	// Native task/Skill/file bookkeeping has no qualified public item mapping.
	if payload.Observation == nil {
		return nil
	}
	s.emit(proto.TypeToolCall, payload)
	return nil
}

// nativeToolObservation projects the native bash and web_fetch tools. A tool
// without its arguments yet has no observation.
func (s *Session) nativeToolObservation(update toolUpdate, stage string) *proto.ToolObservation {
	switch update.Name {
	case "bash":
		command, _ := update.RawInput["command"].(string)
		if command == "" {
			return nil
		}
		cwd := s.opts.Dir
		result := &proto.ToolObservation{Kind: "command", Command: command, Cwd: &cwd, Status: "in_progress"}
		if stage == "after" {
			result.Status = update.Status
			result.Output, _ = json.Marshal(toolResultText(update.RawOutput))
			// Only a finished command reports its exit code; a failed one carries prose, which is not parsed.
			if code, ok := nativeExitCode(update.RawOutput); ok {
				result.ExitCode = &code
			}
		}
		return result
	case "web_fetch":
		url, _ := update.RawInput["url"].(string)
		if url == "" {
			return nil
		}
		result := &proto.ToolObservation{Kind: "web_search", Status: "in_progress", Action: &proto.ToolWebSearchAction{Type: "open_page", URL: &url}}
		if stage == "after" {
			result.Status = update.Status
		}
		return result
	}
	return nil
}

// toolResultText joins the text parts of a native tool result; a Subagent history keeps plain text results.
func toolResultText(output any) string {
	if text, ok := output.(string); ok {
		return text
	}
	result, _ := output.(map[string]any)
	parts, _ := result["content"].([]any)
	var text strings.Builder
	for _, part := range parts {
		if item, _ := part.(map[string]any); item["type"] == "text" {
			value, _ := item["text"].(string)
			text.WriteString(value)
		}
	}
	return text.String()
}

// nativeExitCode reads the structured exit code of a finished bash command.
func nativeExitCode(output any) (int64, bool) {
	result, _ := output.(map[string]any)
	details, _ := result["details"].(map[string]any)
	process, _ := details["processOutput"].(map[string]any)
	switch code := process["exitCode"].(type) {
	case json.Number:
		value, err := code.Int64()
		return value, err == nil
	case float64:
		return int64(code), code == math.Trunc(code)
	}
	return 0, false
}
