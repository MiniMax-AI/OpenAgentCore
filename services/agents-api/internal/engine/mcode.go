package engine

import (
	"errors"
	"strings"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

// WhitespaceOnlyText stays unqualified: the native runtime refuses such prompts
// with "Local message content or attachments are required.".
func mcodeProfile() Profile {
	return Profile{MCPOrigins: []string{"environment"}, MCPBearer: true, ProgrammaticToolCallingDisable: true, Placements: []string{"none", "openai_hosted", "self_hosted"}, ValidateConfiguration: func(a v1.Agent, e *v1.Environment, daemon bool) error {
		if e == nil || (e.Type != "none" && e.Type != "openai_hosted" && e.Type != "self_hosted") || daemon || strings.TrimSpace(a.Model) == "" || a.Reasoning.Effort != nil || a.Reasoning.Summary != nil || (a.ServiceTier != "" && a.ServiceTier != "auto") || (a.Text.Format.Type != "" && a.Text.Format.Type != "text") || (a.Text.Verbosity != "" && a.Text.Verbosity != "medium") {
			return ErrInvalidInput
		}
		return rejectSubagentTools(a, "mcp")
	}, ValidateTools: func(_ *v1.Environment, _ bool, functions []proto.FunctionTool, mcp []proto.MCPHTTPServer) error {
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
