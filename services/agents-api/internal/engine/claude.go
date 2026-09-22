package engine

import (
	"encoding/json"
	"errors"
	"strings"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func claudeProfile() Profile {
	return Profile{
		StructuredOutput:       true,
		ToolSearch:             true,
		MessageImagePlacements: []string{"none"},
		Placements:             []string{"none", "openai_hosted", "self_hosted"}, MCPBearer: true,
		ValidateConfiguration: validateClaudeConfiguration,
		ValidateTools:         validateClaudeTools,
		ValidateFunctionResult: func(content []proto.InputContent) error {
			for _, part := range content {
				if part.Type != "input_text" {
					return ErrInvalidInput
				}
			}
			return nil
		},
	}
}

func validateClaudeConfiguration(agent v1.Agent, environment *v1.Environment, hasDaemon bool) error {
	if environment == nil || (environment.Type != "none" && environment.Type != "openai_hosted" && environment.Type != "self_hosted") || hasDaemon || strings.TrimSpace(agent.Model) == "" {
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
		if environment.Type != "none" || agent.MultiAgent.Enabled {
			return errors.New("Structured output currently requires a single-agent environment:none profile.")
		}
		for _, raw := range agent.Tools {
			var tool struct {
				Type string `json:"type"`
			}
			if json.Unmarshal(raw, &tool) != nil || tool.Type != "function" {
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
		otherTools = otherTools || (tool.Type != "function" && tool.Type != "tool_search")
	}
	if search && (environment.Type != "none" || agent.MultiAgent.Enabled || otherTools || agent.Text.Format.Type == "json_schema") {
		return errors.New("Tool discovery currently requires a single-agent environment:none function profile.")
	}
	return rejectSubagentTools(agent, "function", "mcp")
}

func validateClaudeTools(environment *v1.Environment, _ bool, tools []proto.FunctionTool, mcp []proto.MCPHTTPServer) error {
	if environment.Type != "none" && len(mcp) != 0 {
		return errors.New("The configured workspace profile does not support HTTP MCP tools.")
	}
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
