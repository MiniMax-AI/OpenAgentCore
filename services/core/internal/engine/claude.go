package engine

import (
	"encoding/json"
	"errors"
	"strings"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

// WhitespaceOnlyText stays unqualified: the bridge and Anthropic-compatible
// providers reject text without non-whitespace characters.
func claudeProfile() Profile {
	return Profile{
		ProgrammaticToolCallingDisable: proto.CapabilitySupported,
		MCPOrigins:                     []string{"service", "environment"},
		Placements:                     []string{"none", "openai_hosted", "self_hosted"},
		StructuredOutput:               proto.CapabilitySupported,
		ToolSearch:                     proto.CapabilitySupported,
		MessageImages:                  proto.CapabilitySupported,
		WhitespaceOnlyText:             proto.CapabilityUnsupported,
		WebSearchControl:               proto.CapabilityUnsupported,
		TextVerbosity:                  proto.CapabilityUnsupported,
		MCPBearer:                      proto.CapabilitySupported,
		ConfigurationValidation:        AdditionalValidation,
		ToolsValidation:                AdditionalValidation,
		FunctionResultValidation:       AdditionalValidation,
		ValidateConfiguration:          validateClaudeConfiguration,
		ValidateTools:                  validateClaudeTools,
		ValidateFunctionResult: func(_ string, result proto.FunctionResultPayload) error {
			for _, part := range result.Content {
				if part.Type == "input_image" && !result.Success {
					return ErrInvalidInput
				}
			}
			if (proto.MessageInput{{Content: result.Content}}).ValidateInlineImages() != nil {
				return ErrInvalidInput
			}
			return nil
		},
	}
}

func validateClaudeConfiguration(agent v1.Agent, environment *v1.Environment) error {
	if environment == nil || (environment.Type != "none" && environment.Type != "openai_hosted" && environment.Type != "self_hosted") || strings.TrimSpace(agent.Model) == "" {
		return ErrInvalidInput
	}
	if agent.Text.Verbosity != "" && agent.Text.Verbosity != "medium" {
		return errors.New("The configured engine currently supports medium text verbosity only.")
	}
	if agent.Reasoning.Effort != nil || agent.Reasoning.Summary != nil || (agent.ServiceTier != "" && agent.ServiceTier != "auto") {
		return ErrInvalidInput
	}
	if agent.Text.Format.Type == "json_schema" {
		if err := proto.ValidateBinary64Schema(agent.Text.Format.Schema); err != nil {
			return err
		}
		if agent.MultiAgent.Enabled {
			return errors.New("Structured output requires a single Agent.")
		}
		if len(environment.Skills) != 0 || len(environment.Plugins) != 0 || len(environment.CapabilityDirectories) != 0 {
			return errors.New("Structured output with environment Skills or Plugins is not qualified.")
		}
		for _, raw := range agent.Tools {
			var tool struct {
				Type string `json:"type"`
			}
			if json.Unmarshal(raw, &tool) != nil || (tool.Type != "function" && tool.Type != "web_search" && tool.Type != "programmatic_tool_calling") {
				return ErrInvalidInput
			}
		}
	} else if agent.Text.Format.Type != "" && agent.Text.Format.Type != "text" {
		return ErrInvalidInput
	}
	search, otherTools := false, false
	for _, raw := range agent.Tools {
		var tool struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(raw, &tool) != nil {
			return ErrInvalidInput
		}
		search = search || tool.Type == "tool_search"
		otherTools = otherTools || (tool.Type != "function" && tool.Type != "tool_search" && tool.Type != "web_search" && tool.Type != "programmatic_tool_calling")
	}
	if search && (agent.MultiAgent.Enabled || otherTools || len(environment.Skills) != 0 || len(environment.Plugins) != 0 || len(environment.CapabilityDirectories) != 0 || agent.Text.Format.Type == "json_schema") {
		return errors.New("Tool discovery requires a single-agent function profile without Skills or Plugins.")
	}
	return rejectSubagentTools(agent, "function", "mcp")
}

func validateClaudeTools(environment *v1.Environment, tools []proto.FunctionTool, mcp []proto.MCPHTTPServer) error {
	if err := validateClaudeMCP(mcp); err != nil {
		return err
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
