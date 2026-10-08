package api

import (
	"encoding/json"
	"errors"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
)

// resolveSavedTools canonicalizes tools whose pinned shape was checked at the
// /v1 boundary. The default case guards a pinned tool type that Core does not
// resolve yet.
func resolveSavedTools(input []json.RawMessage) ([]json.RawMessage, error) {
	tools := make([]json.RawMessage, 0, len(input))
	for _, raw := range input {
		var kind struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(raw, &kind); err != nil {
			return nil, &storedDataError{err}
		}
		var value json.RawMessage
		switch kind.Type {
		case "function":
			var function v1.FunctionToolInput
			if err := json.Unmarshal(raw, &function); err != nil {
				return nil, &storedDataError{err}
			}
			resolved, err := resolveFunction(function)
			if err != nil {
				return nil, err
			}
			value = resolved
		case "tool_search":
			value, _ = json.Marshal(kind)
		case "programmatic_tool_calling":
			resolved, _, err := resolveProgrammaticTool(raw)
			if err != nil {
				return nil, err
			}
			value = resolved
		case "mcp":
			resolved, err := resolveMCPTool(raw, true)
			if err != nil {
				return nil, err
			}
			value = resolved
		case "web_search":
			resolved, err := resolveSavedWebSearch(raw)
			if err != nil {
				return nil, err
			}
			value = resolved
		default:
			return nil, errors.New("Unknown persisted tool type.")
		}
		tools = append(tools, value)
	}
	return tools, nil
}
