package claudesdk

import (
	"encoding/json"
	"errors"
	"regexp"
	"strings"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig"
)

var (
	mcpLabel = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)
	mcpTool  = regexp.MustCompile(`^[a-zA-Z0-9_.-]+$`)
)

// validateConfiguration holds the packaged adapter's execution limits on a saved
// Agent and Environment, not saved-Agent schema rules.
func validateConfiguration(admission harnessconfig.Admission) error {
	environment := admission.Environment
	if environment == nil || (environment.Type != "none" && environment.Type != "openai_hosted" && environment.Type != "self_hosted") || strings.TrimSpace(admission.Model) == "" {
		return harnessconfig.ErrInvalidInput
	}
	if admission.TextVerbosity != "" && admission.TextVerbosity != "medium" {
		return errors.New("The configured engine currently supports medium text verbosity only.")
	}
	if admission.Reasoning || (admission.ServiceTier != "" && admission.ServiceTier != "auto") {
		return harnessconfig.ErrInvalidInput
	}
	if admission.TextFormat == "json_schema" {
		if err := proto.ValidateBinary64Schema(admission.TextSchema); err != nil {
			return err
		}
		if admission.MultiAgent {
			return errors.New("Structured output requires a single Agent.")
		}
		if environment.Extensions {
			return errors.New("Structured output with environment Skills or Plugins is not supported.")
		}
		for _, raw := range admission.Tools {
			var tool struct {
				Type string `json:"type"`
			}
			if json.Unmarshal(raw, &tool) != nil || (tool.Type != "function" && tool.Type != "web_search" && tool.Type != "programmatic_tool_calling") {
				return harnessconfig.ErrInvalidInput
			}
		}
	} else if admission.TextFormat != "" && admission.TextFormat != "text" {
		return harnessconfig.ErrInvalidInput
	}
	search, otherTools := false, false
	for _, raw := range admission.Tools {
		var tool struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(raw, &tool) != nil {
			return harnessconfig.ErrInvalidInput
		}
		search = search || tool.Type == "tool_search"
		otherTools = otherTools || (tool.Type != "function" && tool.Type != "tool_search" && tool.Type != "web_search" && tool.Type != "programmatic_tool_calling")
	}
	if search && (admission.MultiAgent || otherTools || environment.Extensions || admission.TextFormat == "json_schema") {
		return errors.New("Tool discovery requires a single-agent function profile without Skills or Plugins.")
	}
	return harnessconfig.RejectSubagentTools(admission, "function", "mcp")
}

// validateTools applies execution limits of the packaged adapter. Shared
// resolution still owns URL, transport and credential-binding validation.
func validateTools(tools []proto.FunctionTool, servers []proto.MCPHTTPServer) error {
	for _, server := range servers {
		if !mcpLabel.MatchString(server.ServerLabel) || server.ServerLabel == "functions" || strings.ContainsAny(server.ServerURL, "?#") {
			return errors.New("The configured engine does not support this HTTP MCP declaration.")
		}
		if server.AllowedTools != nil {
			for _, name := range *server.AllowedTools {
				if !mcpTool.MatchString(name) {
					return errors.New("The configured engine does not support this MCP tool name.")
				}
			}
		}
	}
	for _, tool := range tools {
		var schema struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(tool.Parameters, &schema) != nil || schema.Type != "object" {
			return errors.New("The configured engine currently requires function schemas with root type object.")
		}
	}
	return nil
}

func validateFunctionResult(result proto.FunctionResultPayload) error {
	for _, part := range result.Content {
		if part.Type == "input_image" && !result.Success {
			return harnessconfig.ErrInvalidInput
		}
	}
	if (proto.MessageInput{{Content: result.Content}}).ValidateInlineImages() != nil {
		return harnessconfig.ErrInvalidInput
	}
	return nil
}
