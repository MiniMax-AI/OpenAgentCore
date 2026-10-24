package api

import (
	"encoding/json"
	"errors"
	"fmt"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
)

func resolveSessionTools(input []json.RawMessage) ([]json.RawMessage, error) {
	tools := make([]json.RawMessage, len(input))
	functions := make([]v1.FunctionToolInput, 0, len(input))
	positions := make([]int, 0, len(input))
	search := false
	controls := map[string]bool{}
	for i, raw := range input {
		var kind struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(raw, &kind); err != nil {
			return nil, &storedDataError{err}
		}
		switch kind.Type {
		case "programmatic_tool_calling", "web_search":
			if controls[kind.Type] {
				return nil, errors.New("Execution requires distinct tool controls.")
			}
			controls[kind.Type] = true
			resolve := resolveProgrammaticTool
			if kind.Type == "web_search" {
				resolve = resolveWebSearch
			}
			resolved, err := resolve(raw)
			if err != nil {
				return nil, err
			}
			tools[i] = resolved
		case "tool_search":
			if search {
				return nil, errors.New("Execution requires one tool_search declaration.")
			}
			search = true
			tools[i], _ = json.Marshal(kind)
		case "mcp":
			resolved, err := resolveMCPTool(raw, fmt.Sprintf("tools[%d]", i), false)
			if err != nil {
				return nil, err
			}
			tools[i] = resolved
		case "function":
			var function v1.FunctionToolInput
			if err := json.Unmarshal(raw, &function); err != nil {
				return nil, &storedDataError{err}
			}
			functions = append(functions, function)
			positions = append(positions, i)
		default:
			return nil, errors.New("Unsupported execution tool; supported tools include functions, qualified tool_search, qualified HTTP MCP and execution controls.")
		}
	}
	resolved, err := resolveFunctions(functions)
	if err != nil {
		return nil, err
	}
	for i, value := range resolved {
		tools[positions[i]] = value
	}
	return tools, nil
}
