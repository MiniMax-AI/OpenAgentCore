package engine

import (
	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
)

func codexProfile() Profile {
	return Profile{
		ProgrammaticToolCallingDisable: true,
		Placements:                     []string{"none", "self_hosted", "openai_hosted"},
		MCPOrigins:                     []string{"service", "environment"},
		MessageImages:                  true,
		WhitespaceOnlyText:             true,
		WebSearchControl:               true, TextVerbosity: true, MCPBearer: true,
		ValidateConfiguration: func(agent v1.Agent, _ *v1.Environment, _ bool) error {
			return rejectSubagentTools(agent, "function", "mcp")
		},
	}
}
