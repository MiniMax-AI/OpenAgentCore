package engine

import (
	"errors"
	"strings"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

// WhitespaceOnlyText stays unqualified: the native runtime refuses such prompts
// with "Local message content or attachments are required.".
func mcodeProfile() Profile {
	return Profile{
		MCPOrigins:                     []string{"environment"},
		Placements:                     []string{"none", "openai_hosted", "self_hosted"},
		MCPBearer:                      proto.CapabilitySupported,
		ProgrammaticToolCallingDisable: proto.CapabilitySupported,
		StructuredOutput:               proto.CapabilityUnsupported,
		ToolSearch:                     proto.CapabilityUnsupported,
		MessageImages:                  proto.CapabilityUnsupported,
		WhitespaceOnlyText:             proto.CapabilityUnsupported,
		WebSearchControl:               proto.CapabilityUnsupported,
		TextVerbosity:                  proto.CapabilityUnsupported,
		ConfigurationValidation:        AdditionalValidation,
		ToolsValidation:                AdditionalValidation,
		FunctionResultValidation:       CommonValidationOnly,
		ValidateConfiguration: func(a v1.Agent, e *v1.Environment) error {
			if e == nil || (e.Type != "none" && e.Type != "openai_hosted" && e.Type != "self_hosted") || strings.TrimSpace(a.Model) == "" || a.Reasoning.Effort != nil || a.Reasoning.Summary != nil || (a.ServiceTier != "" && a.ServiceTier != "auto") || (a.Text.Format.Type != "" && a.Text.Format.Type != "text") || (a.Text.Verbosity != "" && a.Text.Verbosity != "medium") {
				return ErrInvalidInput
			}
			return rejectSubagentTools(a, "mcp")
		}, ValidateTools: func(_ *v1.Environment, functions []proto.FunctionTool, mcp []proto.MCPHTTPServer) error {
			if len(functions) > 0 {
				return errors.New("The configured engine does not support public functions.")
			}
			for _, server := range mcp {
				if server.ServerLabel == "oac_workspace" || server.AllowedTools != nil || server.Required {
					return errors.New("The configured engine requires an unreserved MCP label, allowed_tools=null and required=false.")
				}
			}
			return nil
		}}
}
