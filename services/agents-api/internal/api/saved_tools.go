package api

import (
	"encoding/json"
	"errors"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
)

func resolveSavedTools(input []json.RawMessage) ([]json.RawMessage, error) {
	tools := make([]json.RawMessage, 0, len(input))
	for _, raw := range input {
		var kind struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(raw, &kind) != nil {
			return nil, errors.New("tools must contain tool objects.")
		}
		var value json.RawMessage
		switch kind.Type {
		case "function":
			var function v1.FunctionToolInput
			if decodeInputObject(raw, &function, "type", "name", "description", "parameters", "defer_loading") != nil {
				return nil, errors.New("Invalid function tool fields.")
			}
			resolved, _, err := resolveFunction(function)
			if err != nil {
				return nil, err
			}
			value = resolved
		case "tool_search":
			if decodeInputObject(raw, &kind, "type") != nil {
				return nil, errors.New("tool_search only accepts type.")
			}
			value, _ = json.Marshal(kind)
		case "programmatic_tool_calling":
			resolved, err := resolveProgrammaticTool(raw)
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
			resolved, err := resolveDisabledWebSearch(raw)
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
