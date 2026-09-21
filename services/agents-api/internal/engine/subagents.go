package engine

import (
	"encoding/json"
	"errors"
	"slices"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
)

// rejectSubagentTools keeps unqualified native combinations in adapter profiles.
func rejectSubagentTools(agent v1.Agent, unsupported ...string) error {
	if !agent.MultiAgent.Enabled {
		return nil
	}
	for _, raw := range agent.Tools {
		var tool struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(raw, &tool) != nil {
			return ErrInvalidInput
		}
		if slices.Contains(unsupported, tool.Type) {
			return errors.New("The configured Subagent profile does not support this tool combination.")
		}
	}
	return nil
}
