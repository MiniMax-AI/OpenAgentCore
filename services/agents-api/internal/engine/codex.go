package engine

import (
	"errors"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func codexProfile() Profile {
	return Profile{
		ProgrammaticToolCallingDisable: true,
		Placements:                     []string{"none", "self_hosted", "openai_hosted"},
		MessageImages:                  true,
		WhitespaceOnlyText:             true,
		WebSearchControl:               true, TextVerbosity: true, MCPBearer: true,
		ValidateConfiguration: func(agent v1.Agent, _ *v1.Environment, _ bool) error {
			return rejectSubagentTools(agent, "function", "mcp")
		},
		ValidateTools: func(environment *v1.Environment, hasDaemon bool, _ []proto.FunctionTool, mcp []proto.MCPHTTPServer) error {
			if len(mcp) != 0 && (environment == nil || environment.Type != "none" || hasDaemon) {
				return errors.New("HTTP MCP execution currently requires the Codex service-side environment:none profile")
			}
			return nil
		},
	}
}
