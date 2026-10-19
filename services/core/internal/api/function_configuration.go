package api

import (
	"encoding/json"
	"errors"
	"strings"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
)

func resolveFunctions(input []v1.FunctionToolInput) ([]json.RawMessage, error) {
	tools := make([]json.RawMessage, 0, len(input))
	names := make(map[string]bool, len(input))
	for _, tool := range input {
		if strings.TrimSpace(tool.Name) == "" || names[tool.Name] {
			return nil, errors.New("Function names must be nonempty and unique.")
		}
		value, err := resolveFunction(tool)
		if err != nil {
			return nil, err
		}
		names[tool.Name] = true
		tools = append(tools, value)
	}
	return tools, nil
}

// resolveFunction canonicalizes a function whose pinned shape was checked at
// the /v1 boundary. Execution admission may impose additional restrictions,
// without narrowing the reusable resource.
func resolveFunction(tool v1.FunctionToolInput) (json.RawMessage, error) {
	return json.Marshal(struct {
		Type         string          `json:"type"`
		Name         string          `json:"name"`
		Description  string          `json:"description"`
		Parameters   json.RawMessage `json:"parameters"`
		DeferLoading bool            `json:"defer_loading"`
	}{"function", tool.Name, tool.Description, tool.Parameters, tool.DeferLoading != nil && *tool.DeferLoading})
}
