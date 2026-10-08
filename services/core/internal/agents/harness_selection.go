package agents

import (
	"encoding/json"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

// HarnessSelection projects an Agent and its Environment, if known, onto what
// proto.ValidateSelection checks against a Harness declaration. Callers
// validate the tool declarations themselves. An MCP server with a
// credential_id is authenticated.
func HarnessSelection(agent v1.Agent, environment *v1.Environment) proto.Selection {
	selection := proto.Selection{MultiAgent: agent.MultiAgent.Enabled, TextVerbosity: agent.Text.Verbosity}
	if agent.Text.Format.Type == "json_schema" {
		selection.OutputSchema = agent.Text.Format.Schema
	}
	if environment != nil {
		selection.Environment = "local"
		if environment.Type == "none" {
			selection.Environment = "none"
		}
		selection.InstalledCapabilities = len(environment.Skills) > 0 || len(environment.Plugins) > 0 || len(environment.CapabilityDirectories) > 0
	}
	for _, raw := range agent.Tools {
		var tool struct {
			Type             string    `json:"type"`
			DeferLoading     bool      `json:"defer_loading"`
			ServerLabel      string    `json:"server_label"`
			AllowedTools     *[]string `json:"allowed_tools"`
			ConnectionOrigin string    `json:"connection_origin"`
			CredentialID     *string   `json:"credential_id"`
			Required         bool      `json:"required"`
		}
		if json.Unmarshal(raw, &tool) != nil {
			continue
		}
		switch tool.Type {
		case "function":
			selection.Functions = true
			selection.DeferredFunctions = selection.DeferredFunctions || tool.DeferLoading
		case "tool_search":
			selection.ToolSearch = true
		case "mcp":
			selection.MCP = append(selection.MCP, proto.SelectedMCP{Origin: tool.ConnectionOrigin, Label: tool.ServerLabel,
				AllowedTools: tool.AllowedTools, Required: tool.Required, Bearer: tool.CredentialID != nil})
		}
	}
	return selection
}
