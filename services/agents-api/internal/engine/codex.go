package engine

import (
	"encoding/json"
	"errors"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func codexProfile() Profile {
	return Profile{
		Placements:       []string{"none", "self_hosted", "openai_hosted"},
		WebSearchControl: true, TextVerbosity: true, MCPBearer: true,
		ValidateConfiguration: func(agent v1.Agent, _ *v1.Environment, _ bool) error {
			if agent.MultiAgent.Enabled {
				for _, tool := range agent.Tools {
					var definition struct {
						Type string `json:"type"`
					}
					if json.Unmarshal(tool, &definition) != nil {
						return ErrInvalidInput
					}
					if definition.Type == "function" {
						return errors.New("Subagent execution with public function tools is not supported yet.")
					}
				}
			}
			return nil
		},
		ValidateTools: func(environment *v1.Environment, hasDaemon bool, _ []proto.FunctionTool, mcp []proto.MCPHTTPServer) error {
			if len(mcp) != 0 && (environment == nil || environment.Type != "none" || hasDaemon) {
				return errors.New("HTTP MCP execution currently requires the Codex service-side environment:none profile")
			}
			return nil
		},
	}
}
